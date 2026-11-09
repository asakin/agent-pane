package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent-pane/internal/herdrcore"
)

// The Options setting that picks the backend new sessions run in. Built-in
// is the default and the fallback; Durable is the preview that runs them in
// the bundled core (RFC 0001).
const (
	terminalBackendKey     = "terminalBackend"
	terminalBackendBuiltin = "builtin"
	terminalBackendDurable = "durable"
)

// coreCallTimeout bounds one call to the core, so a core that stopped
// answering fails a tty.* request instead of hanging it.
const coreCallTimeout = 10 * time.Second

// coreStartTimeout bounds starting a session, the core included when it is
// not running yet.
const coreStartTimeout = 30 * time.Second

// coreHistoryLines is how much scrollback a viewer's first paint carries:
// xterm's default scrollback, which the side panel uses.
const coreHistoryLines = 1000

// coreWatchRetryMax bounds the wait between attempts to watch a terminal
// again after its event subscription broke.
const coreWatchRetryMax = 30 * time.Second

// coreAPI is the part of a core connection the backend uses. coreClient is
// the real one; tests use a fake.
type coreAPI interface {
	Run(ctx context.Context, p herdrcore.RunParams) (herdrcore.Terminal, error)
	List(ctx context.Context, labels map[string]string, all bool) ([]herdrcore.Terminal, error)
	Attach(ctx context.Context, p herdrcore.AttachParams) (coreStream, error)
	Subscribe(ctx context.Context, labels map[string]string) (coreEvents, error)
	Kill(ctx context.Context, terminal string) (herdrcore.Terminal, error)
	Remove(ctx context.Context, terminal string) error
	Done() <-chan struct{}
}

// coreStream is one viewer's attach stream: the first paint, then live
// output. Writes are input.
type coreStream interface {
	io.ReadWriteCloser
	// ReadFrame returns the next output frame whole.
	ReadFrame() ([]byte, error)
	Resize(cols, rows int) error
}

// coreEvents is a subscription to one terminal's fact changes.
type coreEvents interface {
	Snapshot() []herdrcore.Terminal
	Next(ctx context.Context) (herdrcore.Event, error)
	Close() error
}

// coreClient adapts a herdrcore.Client to coreAPI.
type coreClient struct{ *herdrcore.Client }

func (c coreClient) Attach(ctx context.Context, p herdrcore.AttachParams) (coreStream, error) {
	return c.Client.Attach(ctx, p)
}

func (c coreClient) Subscribe(ctx context.Context, labels map[string]string) (coreEvents, error) {
	s, err := c.Client.Subscribe(ctx, labels)
	if err != nil {
		return nil, err
	}
	return coreSubscription{s}, nil
}

type coreSubscription struct{ *herdrcore.Subscription }

func (s coreSubscription) Snapshot() []herdrcore.Terminal { return s.Terminals }

// errCoreMissing reports a Durable session with no bundled core installed.
var errCoreMissing = errors.New("the bundled terminal runtime is not installed; run `agent-pane doctor`")

// errCoreUnsure reports a start whose outcome the host could not learn: the
// program may be running in the core, so the session must not start it
// again in Built-in.
var errCoreUnsure = errors.New("the bundled terminal runtime did not say whether the terminal started")

// coreBackend runs each session in a terminal of the private core, reached
// over one shared connection that is redialled once it drops. The host still
// owns the session id, pane tab, attach tokens and page socket.
type coreBackend struct {
	logger *slog.Logger
	// dial starts the core if needed and connects to it.
	dial func(ctx context.Context) (coreAPI, error)

	mu   sync.Mutex
	conn coreAPI

	// unsure holds the agent-pane.session labels of starts that may have
	// left a program running in the core and that the host has not yet
	// confirmed ended; guarded by unsureMu.
	unsureMu sync.Mutex
	unsure   map[string]struct{}
}

func newCoreBackend(logger *slog.Logger, core herdrcore.Core) *coreBackend {
	// The core's own environment is what its terminals start from, so it
	// gets none of a session's reserved keys.
	base := withSessionEnv(os.Environ(), nil)
	return &coreBackend{
		logger: logger,
		dial: func(ctx context.Context) (coreAPI, error) {
			if _, err := os.Stat(core.Binary); err != nil {
				return nil, errCoreMissing
			}
			if err := core.StartEnv(ctx, base); err != nil {
				return nil, err
			}
			c, err := core.Dial(ctx)
			if err != nil {
				return nil, err
			}
			return coreClient{c}, nil
		},
	}
}

// connect returns the open connection, dialling a new one if there is none
// or the last one ended.
func (b *coreBackend) connect(ctx context.Context) (coreAPI, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		select {
		case <-b.conn.Done():
			b.conn = nil
		default:
			return b.conn, nil
		}
	}
	conn, err := b.dial(ctx)
	if err != nil {
		return nil, err
	}
	b.conn = conn
	return conn, nil
}

// keepUnsure accounts for a start that may have left a program running
// under the session label: it is ended in the background, retrying until the
// core confirms, and no Durable session starts until then.
func (b *coreBackend) keepUnsure(session string) {
	b.unsureMu.Lock()
	if b.unsure == nil {
		b.unsure = map[string]struct{}{}
	}
	_, kept := b.unsure[session]
	b.unsure[session] = struct{}{}
	b.unsureMu.Unlock()
	if kept {
		return
	}
	go func() {
		delay := 100 * time.Millisecond
		for {
			time.Sleep(delay)
			delay = min(2*delay, coreWatchRetryMax)
			err := b.endUnsure(session)
			if err == nil {
				return
			}
			b.logger.Debug("failed to end core terminal", "error", err, "session", session)
		}
	}()
}

// endUnsure kills and removes every terminal carrying the session label,
// finished ones included, and forgets the label once the core confirms it.
func (b *coreBackend) endUnsure(session string) error {
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
	defer cancel()
	conn, err := b.connect(ctx)
	if err != nil {
		return err
	}
	terms, err := conn.List(ctx, map[string]string{coreSessionLabel: session}, true)
	if err != nil {
		return err
	}
	for _, term := range terms {
		if _, err := conn.Kill(ctx, term.Address); err != nil && !herdrcore.IsKind(err, "not_found") {
			return err
		}
		if err := conn.Remove(ctx, term.Address); err != nil && !herdrcore.IsKind(err, "not_found") {
			return err
		}
	}
	b.unsureMu.Lock()
	delete(b.unsure, session)
	b.unsureMu.Unlock()
	return nil
}

// settle ends every start still unsure. Its error is errCoreUnsure: a
// program may still run, so nothing should start in its place.
func (b *coreBackend) settle() error {
	b.unsureMu.Lock()
	sessions := make([]string, 0, len(b.unsure))
	for session := range b.unsure {
		sessions = append(sessions, session)
	}
	b.unsureMu.Unlock()
	for _, session := range sessions {
		if err := b.endUnsure(session); err != nil {
			return fmt.Errorf("%w: an earlier terminal may still be running: %w", errCoreUnsure, err)
		}
	}
	return nil
}

func (b *coreBackend) open(id string, command *terminalCommand) (terminal, error) {
	return &coreTerminal{
		id:      id,
		backend: b,
		logger:  b.logger,
		done:    make(chan struct{}),
		cols:    command.Cols,
		rows:    command.Rows,
	}, nil
}

// inheritsPageChannel is false: the core cannot pass a descriptor to the
// program, so its processes reach the page through the session's socket.
func (b *coreBackend) inheritsPageChannel() bool { return false }

// coreTerminal is one session's terminal in the core.
type coreTerminal struct {
	id      string
	backend *coreBackend
	logger  *slog.Logger
	// done is closed by close.
	done chan struct{}

	mu      sync.Mutex
	address string
	// session is the terminal's agent-pane.session label.
	session string
	events  coreEvents
	cols    int
	rows    int
	closed  bool
	viewer  terminalViewer
	stream  coreStream
}

func (t *coreTerminal) start(command *terminalCommand, extra *os.File) (int, error) {
	if err := t.backend.settle(); err != nil {
		return 0, err
	}
	// Long enough for the first session to start the core.
	ctx, cancel := context.WithTimeout(context.Background(), coreStartTimeout)
	defer cancel()
	conn, err := t.backend.connect(ctx)
	if err != nil {
		return 0, err
	}
	env := coreEnv(command.Env)
	labels := coreLabels(env)
	cols, rows := t.size()
	t.mu.Lock()
	t.session = labels[coreSessionLabel]
	t.mu.Unlock()
	term, err := conn.Run(ctx, herdrcore.RunParams{
		Argv:     command.Args,
		Cwd:      command.Dir,
		Env:      env,
		Labels:   labels,
		Size:     &herdrcore.Size{Cols: cols, Rows: rows},
		NoParent: true,
	})
	if err != nil {
		var refused *herdrcore.Error
		if errors.As(err, &refused) {
			// The core answered: nothing started.
			return 0, err
		}
		// No answer: the program may be running. End whatever runs under
		// the session's label, and do not start it anywhere else.
		if cleanupErr := t.backend.endUnsure(t.session); cleanupErr != nil {
			t.logger.Warn("failed to end core terminal", "error", cleanupErr, "session", t.id)
			t.backend.keepUnsure(t.session)
		}
		return 0, fmt.Errorf("%w: %w", errCoreUnsure, err)
	}
	t.mu.Lock()
	t.address = term.Address
	closed := t.closed
	t.mu.Unlock()
	if closed {
		// close ran while the core started it, before it knew the address.
		return 0, t.end(conn, errCoreTerminalClosed)
	}
	// Watched from here, so an exit before run is not missed.
	events, err := conn.Subscribe(ctx, map[string]string{coreSessionLabel: t.session})
	if err != nil {
		return 0, t.end(conn, err)
	}
	if !t.setEvents(events) {
		return 0, t.end(conn, errCoreTerminalClosed)
	}
	pid := 0
	if term.Pid != nil {
		pid = *term.Pid
	}
	return pid, nil
}

// size is the terminal's size, 80x24 until the panel says otherwise.
func (t *coreTerminal) size() (int, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cols < 1 || t.rows < 1 {
		return 80, 24
	}
	return t.cols, t.rows
}

// setEvents makes events the terminal's subscription, unless it is closed.
func (t *coreTerminal) setEvents(events coreEvents) bool {
	t.mu.Lock()
	closed := t.closed
	if !closed {
		t.events = events
	}
	t.mu.Unlock()
	if closed {
		_ = events.Close()
	}
	return !closed
}

// run calls ended once the core reports the program ended. Losing the
// subscription is not that: the terminal is watched again from a fresh
// snapshot, and only a closed terminal stops the watch.
func (t *coreTerminal) run(ended func()) {
	t.mu.Lock()
	events, address := t.events, t.address
	t.mu.Unlock()
	go func() {
		defer ended()
		if events == nil {
			return
		}
		for {
			if !coreTerminalLive(events.Snapshot(), address) {
				return
			}
			err := waitForEnd(events, address)
			if err == nil {
				return
			}
			_ = events.Close()
			t.logger.Debug("core events ended; watching the terminal again", "error", err, "session", t.id)
			if events = t.resubscribe(); events == nil {
				return
			}
		}
	}()
}

// waitForEnd returns nil once events report address ended, or the error
// that ended the subscription.
func waitForEnd(events coreEvents, address string) error {
	for {
		e, err := events.Next(context.Background())
		if err != nil {
			return err
		}
		switch e.Type {
		case "finished", "removed", "lost":
			if e.Terminal == address {
				return nil
			}
		case "missed":
			if !coreTerminalLive(e.Terminals, address) {
				return nil
			}
		}
	}
}

// resubscribe subscribes to the terminal's events again, retrying with a
// growing wait until it can or the terminal is closed (nil).
func (t *coreTerminal) resubscribe() coreEvents {
	delay := 100 * time.Millisecond
	for {
		select {
		case <-t.done:
			return nil
		case <-time.After(delay):
		}
		delay = min(2*delay, coreWatchRetryMax)
		ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
		conn, err := t.backend.connect(ctx)
		var events coreEvents
		if err == nil {
			events, err = conn.Subscribe(ctx, map[string]string{coreSessionLabel: t.session})
		}
		cancel()
		if err != nil {
			t.logger.Debug("failed to watch core terminal", "error", err, "session", t.id)
			continue
		}
		if !t.setEvents(events) {
			return nil
		}
		return events
	}
}

func coreTerminalLive(terminals []herdrcore.Terminal, address string) bool {
	for _, t := range terminals {
		if t.Address == address {
			return t.State == "live"
		}
	}
	return false
}

// attach opens a core stream for viewer at the terminal's size. The core's
// first paint (the screen and coreHistoryLines of scrollback) is the replay
// frame; live output follows.
func (t *coreTerminal) attach(viewer terminalViewer) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
	defer cancel()
	conn, err := t.backend.connect(ctx)
	if err != nil {
		return nil, err
	}
	cols, rows := t.size()
	t.mu.Lock()
	address := t.address
	t.mu.Unlock()
	stream, err := conn.Attach(ctx, herdrcore.AttachParams{Terminal: address, Cols: cols, Rows: rows, History: coreHistoryLines})
	if err != nil {
		return nil, err
	}
	// The core sends the first paint as one frame straight after the
	// reply, so that frame, whole, is the replay.
	paint, err := readFrameWithin(stream, coreCallTimeout)
	if err != nil {
		stream.Close()
		return nil, fmt.Errorf("no first paint: %w", err)
	}
	if err := viewer.send(paint); err != nil {
		stream.Close()
		return nil, err
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		stream.Close()
		return nil, errCoreTerminalClosed
	}
	oldViewer, oldStream := t.viewer, t.stream
	t.viewer, t.stream = viewer, stream
	t.mu.Unlock()
	if oldStream != nil {
		_ = oldStream.Close()
	}
	if oldViewer != nil {
		_ = oldViewer.close()
	}

	go t.forward(stream, viewer)
	return func() {
		t.mu.Lock()
		current := t.stream == stream
		if current {
			t.viewer, t.stream = nil, nil
		}
		t.mu.Unlock()
		if current {
			_ = stream.Close()
		}
	}, nil
}

// readFrameWithin is one ReadFrame that gives up after timeout by closing s.
func readFrameWithin(s coreStream, timeout time.Duration) ([]byte, error) {
	timer := time.AfterFunc(timeout, func() { s.Close() })
	defer timer.Stop()
	return s.ReadFrame()
}

// forward copies a stream's live output to its viewer until the stream ends.
func (t *coreTerminal) forward(stream coreStream, viewer terminalViewer) {
	buf := make([]byte, 32<<10)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if sendErr := viewer.send(buf[:n]); sendErr != nil {
				t.logger.Debug("failed to write core output to websocket", "error", sendErr)
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *coreTerminal) write(p []byte) (int, error) {
	t.mu.Lock()
	stream := t.stream
	t.mu.Unlock()
	if stream == nil {
		return 0, errors.New("no viewer attached")
	}
	return stream.Write(p)
}

// resize records the size for the next viewer and sets it through the
// current one: the core sizes a terminal to its last active viewer.
func (t *coreTerminal) resize(cols, rows int) error {
	if cols < 1 || rows < 1 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	stream := t.stream
	t.mu.Unlock()
	if stream == nil {
		return nil
	}
	return stream.Resize(cols, rows)
}

// close ends the program and removes its core terminal.
func (t *coreTerminal) close() error {
	t.mu.Lock()
	if !t.closed {
		close(t.done)
	}
	t.closed = true
	viewer, stream, events := t.viewer, t.stream, t.events
	t.viewer, t.stream, t.events = nil, nil, nil
	t.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
	if viewer != nil {
		_ = viewer.close()
	}
	if events != nil {
		_ = events.Close()
	}
	t.mu.Lock()
	started := t.address != ""
	t.mu.Unlock()
	if !started {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
	defer cancel()
	conn, err := t.backend.connect(ctx)
	if err != nil {
		return err
	}
	return t.endWith(ctx, conn)
}

var errCoreTerminalClosed = errors.New("terminal is closed")

// end kills and removes the terminal after a start that failed with cause.
// When it cannot, the program may still run, so the error says so.
func (t *coreTerminal) end(conn coreAPI, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
	defer cancel()
	if err := t.endWith(ctx, conn); err != nil {
		t.logger.Warn("failed to end core terminal", "error", err, "session", t.id)
		t.backend.keepUnsure(t.session)
		return fmt.Errorf("%w: %w", errCoreUnsure, cause)
	}
	return cause
}

func (t *coreTerminal) endWith(ctx context.Context, conn coreAPI) error {
	t.mu.Lock()
	address := t.address
	t.mu.Unlock()
	if address == "" {
		return nil
	}
	if _, err := conn.Kill(ctx, address); err != nil && !herdrcore.IsKind(err, "not_found") {
		return err
	}
	if err := conn.Remove(ctx, address); err != nil && !herdrcore.IsKind(err, "not_found") {
		return err
	}
	return nil
}

// Labels on a session's core terminal, from the public ids its environment
// already gives programs; the secret pane id never leaves the host.
const (
	corePaneLabel    = "agent-pane.pane"
	coreSessionLabel = "agent-pane.session"
	coreAgentLabel   = "agent-pane.agent"
)

func coreLabels(env map[string]string) map[string]string {
	labels := map[string]string{coreSessionLabel: env[envSessionID]}
	if pane := env[envTabID]; pane != "" {
		labels[corePaneLabel] = pane
	}
	if agent := env[envAgent]; agent != "" {
		labels[coreAgentLabel] = agent
	}
	return labels
}

// coreEnv is a session's environment as terminal.run's env, which the core
// sets on top of its own. The core's HC_* keys stay the core's, so hc in the
// terminal reaches Agent Pane's core. The core runs with private XDG paths;
// the program gets the session's, or the XDG defaults.
func coreEnv(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || strings.HasPrefix(key, "HC_") {
			continue
		}
		out[key] = val
	}
	home := out["HOME"]
	if home == "" {
		home = os.Getenv("HOME")
	}
	if _, ok := out["XDG_CONFIG_HOME"]; !ok {
		out["XDG_CONFIG_HOME"] = filepath.Join(home, ".config")
	}
	if _, ok := out["XDG_STATE_HOME"]; !ok {
		out["XDG_STATE_HOME"] = filepath.Join(home, ".local", "state")
	}
	return out
}
