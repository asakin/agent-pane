package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"agent-pane/internal/scrollback"
	"github.com/aymanbagabas/go-pty"
)

// terminalBackend makes the terminals sessions run in. SessionManager keeps
// the session ids, pane tabs, attach tokens and page channels; the backend
// owns the terminal itself.
type terminalBackend interface {
	// open makes a terminal of command's size for session id. Nothing runs
	// in it until start.
	open(id string, command *terminalCommand) (terminal, error)
	// inheritsPageChannel reports whether a started program can inherit
	// the page channel as fd 3. Without it, the session's page socket is
	// the only way its processes reach the page.
	inheritsPageChannel() bool
}

// terminal is one session's terminal.
type terminal interface {
	// start runs command in the terminal, with extra (the page channel) as
	// fd 3 when it is not nil. pid is the root process page-socket checks
	// run against, 0 when the backend cannot say.
	start(command *terminalCommand, extra *os.File) (pid int, err error)
	// run streams the started terminal's output to its viewer and calls
	// ended, possibly more than once, when the terminal or its program ends.
	run(ended func())
	// attach sends the replay to viewer as one frame, possibly empty, then
	// live output. It replaces and closes any earlier viewer. detach stops
	// output to this viewer if it is still the current one.
	attach(viewer terminalViewer) (detach func(), err error)
	// write sends input to the terminal.
	write(p []byte) (int, error)
	resize(cols, rows int) error
	// close ends the terminal and closes its viewer.
	close() error
}

// terminalViewer receives a terminal's output frames.
type terminalViewer interface {
	send(p []byte) error
	close() error
}

// terminalCommand is the program a session runs, independent of backend.
type terminalCommand struct {
	Path string
	// Args holds the command-line arguments, including the command as Args[0].
	Args []string
	Env  []string
	Dir  string
	Cols int
	Rows int
}

// newTerminalCommand returns the command to run name with args.
func newTerminalCommand(name string, args ...string) *terminalCommand {
	return &terminalCommand{Path: name, Args: append([]string{name}, args...)}
}

// ptyBackend runs each session in a PTY the host owns, with scrollback the
// host keeps for replay.
type ptyBackend struct {
	logger *slog.Logger
}

func (b ptyBackend) open(id string, command *terminalCommand) (terminal, error) {
	tty, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create pty: %w", err)
	}
	if err := tty.Resize(command.Cols, command.Rows); err != nil {
		tty.Close()
		return nil, fmt.Errorf("failed to set size for tty: %w", err)
	}
	t := newPtyTerminal(tty, scrollbackBytes)
	t.id = id
	t.logger = b.logger
	return t, nil
}

func (b ptyBackend) inheritsPageChannel() bool { return true }

type ptyTerminal struct {
	id     string
	logger *slog.Logger
	tty    pty.Pty
	wait   func() error

	// outMu orders scrollback writes with replays, so a replay sees buf
	// and modes at the same point and a viewer gets no live output first.
	outMu sync.Mutex
	buf   *scrollback.Ring
	// modes restores the terminal modes the replay lost once buf wraps.
	modes scrollback.Modes

	mu     sync.Mutex
	viewer terminalViewer
}

func newPtyTerminal(tty pty.Pty, scrollbackSize int) *ptyTerminal {
	return &ptyTerminal{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		tty:    tty,
		buf:    scrollback.New(scrollbackSize),
	}
}

// preface puts p in the scrollback before the program's output, so the
// first viewer sees it.
func (t *ptyTerminal) preface(p []byte) {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	_, _ = t.buf.Write(p)
	t.modes.Write(p)
}

func (t *ptyTerminal) start(command *terminalCommand, extra *os.File) (int, error) {
	cmd := t.tty.Command(command.Path)
	cmd.Args = command.Args
	cmd.Env = command.Env
	cmd.Dir = command.Dir
	wait, pid, err := startPty(t.tty, cmd, extra)
	if err != nil {
		return 0, err
	}
	t.wait = wait
	return pid, nil
}

func (t *ptyTerminal) run(ended func()) {
	go t.readLoop(ended)
	go func() {
		_ = t.wait()
		ended()
	}()
}

func (t *ptyTerminal) readLoop(ended func()) {
	buf := make([]byte, maxBufferSizeBytes)
	for {
		n, err := t.tty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.outMu.Lock()
			_, _ = t.buf.Write(chunk)
			t.modes.Write(chunk)
			t.outMu.Unlock()

			t.mu.Lock()
			viewer := t.viewer
			t.mu.Unlock()
			if viewer != nil {
				if writeErr := viewer.send(chunk); writeErr != nil {
					t.logger.Debug("failed to write tty output to websocket", "error", writeErr)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				t.logger.Debug("pty read ended", "error", err, "session", t.id)
			}
			ended()
			return
		}
	}
}

// replay is the scrollback a new viewer starts with. The caller holds outMu.
func (t *ptyTerminal) replay() []byte {
	snapshot := t.buf.Bytes()
	if snapshot == nil {
		snapshot = []byte{}
	}
	if t.buf.Dropped() {
		prefix, suffix := t.modes.Restore()
		snapshot = append(append([]byte(prefix), snapshot...), suffix...)
	}
	return snapshot
}

func (t *ptyTerminal) attach(viewer terminalViewer) (func(), error) {
	t.outMu.Lock()
	defer t.outMu.Unlock()
	if err := viewer.send(t.replay()); err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.viewer != nil {
		_ = t.viewer.close()
	}
	t.viewer = viewer
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.viewer == viewer {
			t.viewer = nil
		}
	}, nil
}

func (t *ptyTerminal) write(p []byte) (int, error) {
	return t.tty.Write(p)
}

func (t *ptyTerminal) resize(cols, rows int) error {
	return t.tty.Resize(cols, rows)
}

func (t *ptyTerminal) close() error {
	t.mu.Lock()
	if t.viewer != nil {
		_ = t.viewer.close()
		t.viewer = nil
	}
	t.mu.Unlock()
	return t.tty.Close()
}
