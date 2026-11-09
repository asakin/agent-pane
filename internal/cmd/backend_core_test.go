//go:build unix

package cmd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-pane/internal/herdrcore"
	"agent-pane/internal/jsonrpc"
	"github.com/gorilla/websocket"
)

// fakeCore stands in for the bundled core at the coreAPI boundary.
type fakeCore struct {
	mu       sync.Mutex
	runs     []herdrcore.RunParams
	attaches []herdrcore.AttachParams
	killed   []string
	removed  []string
	streams  []*fakeStream
	events   map[string]chan herdrcore.Event
	runErr   error
	// runLost makes Run start the terminal and then fail as a lost reply
	// does. runGate, when set, holds Run until it is closed.
	runLost bool
	runGate chan struct{}
	// listErr, when set, fails List. killErr fails Kill for an address.
	listErr error
	killErr map[string]error
	subs    []*fakeEvents
	pid     int
	done    chan struct{}
}

func newFakeCore() *fakeCore {
	return &fakeCore{events: map[string]chan herdrcore.Event{}, pid: os.Getpid(), done: make(chan struct{})}
}

func (f *fakeCore) Run(_ context.Context, p herdrcore.RunParams) (herdrcore.Terminal, error) {
	if f.runGate != nil {
		<-f.runGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runErr != nil {
		return herdrcore.Terminal{}, f.runErr
	}
	f.runs = append(f.runs, p)
	address := fmt.Sprintf("t%d", len(f.runs))
	f.events[p.Labels[coreSessionLabel]] = make(chan herdrcore.Event, 4)
	pid := f.pid
	if f.runLost {
		return herdrcore.Terminal{}, context.DeadlineExceeded
	}
	return herdrcore.Terminal{Address: address, State: "live", Labels: p.Labels, Pid: &pid}, nil
}

func (f *fakeCore) List(_ context.Context, labels map[string]string, _ bool) ([]herdrcore.Terminal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []herdrcore.Terminal
	for i, run := range f.runs {
		if run.Labels[coreSessionLabel] == labels[coreSessionLabel] {
			out = append(out, herdrcore.Terminal{Address: fmt.Sprintf("t%d", i+1), State: "live"})
		}
	}
	return out, nil
}

func (f *fakeCore) Attach(_ context.Context, p herdrcore.AttachParams) (coreStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attaches = append(f.attaches, p)
	s := &fakeStream{out: make(chan []byte, 8), closed: make(chan struct{})}
	s.out <- []byte("paint of " + p.Terminal)
	f.streams = append(f.streams, s)
	return s, nil
}

func (f *fakeCore) Subscribe(_ context.Context, labels map[string]string) (coreEvents, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := f.events[labels[coreSessionLabel]]
	address := fmt.Sprintf("t%d", len(f.runs))
	e := &fakeEvents{ch: ch, snapshot: []herdrcore.Terminal{{Address: address, State: "live"}}}
	f.subs = append(f.subs, e)
	return e, nil
}

func (f *fakeCore) Kill(_ context.Context, terminal string) (herdrcore.Terminal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.killErr[terminal]; err != nil {
		return herdrcore.Terminal{}, err
	}
	f.killed = append(f.killed, terminal)
	return herdrcore.Terminal{Address: terminal, State: "finished"}, nil
}

func (f *fakeCore) Remove(_ context.Context, terminal string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, terminal)
	return nil
}

func (f *fakeCore) Done() <-chan struct{} { return f.done }

// finish reports the session's terminal finished, as the core does when its
// program exits.
func (f *fakeCore) finish(session, address string) {
	f.mu.Lock()
	ch := f.events[session]
	f.mu.Unlock()
	ch <- herdrcore.Event{Seq: 1, Type: "finished", Terminal: address}
}

// send delivers e on the session's subscription.
func (f *fakeCore) send(session string, e herdrcore.Event) {
	f.mu.Lock()
	ch := f.events[session]
	f.mu.Unlock()
	ch <- e
}

// dropEvents ends the open subscriptions as a dropped connection does.
func (f *fakeCore) dropEvents() {
	f.mu.Lock()
	subs := f.subs
	f.mu.Unlock()
	for _, e := range subs {
		e.Close()
	}
}

func (f *fakeCore) subscriptions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

func (f *fakeCore) stream(i int) *fakeStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[i]
}

type fakeStream struct {
	out    chan []byte
	closed chan struct{}
	once   sync.Once

	mu      sync.Mutex
	input   []string
	resizes [][2]int
}

func (s *fakeStream) Read(p []byte) (int, error) {
	select {
	case b := <-s.out:
		return copy(p, b), nil
	case <-s.closed:
		return 0, herdrcore.ErrStreamClosed
	}
}

func (s *fakeStream) ReadFrame() ([]byte, error) {
	select {
	case b := <-s.out:
		return b, nil
	case <-s.closed:
		return nil, herdrcore.ErrStreamClosed
	}
}

func (s *fakeStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = append(s.input, string(p))
	return len(p), nil
}

func (s *fakeStream) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizes = append(s.resizes, [2]int{cols, rows})
	return nil
}

func (s *fakeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *fakeStream) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

type fakeEvents struct {
	ch       chan herdrcore.Event
	snapshot []herdrcore.Terminal
	once     sync.Once
	closed   chan struct{}
}

func (e *fakeEvents) Snapshot() []herdrcore.Terminal { return e.snapshot }

func (e *fakeEvents) Next(ctx context.Context) (herdrcore.Event, error) {
	e.once.Do(func() { e.closed = make(chan struct{}) })
	select {
	case ev := <-e.ch:
		return ev, nil
	case <-e.closed:
		return herdrcore.Event{}, herdrcore.ErrStreamClosed
	case <-ctx.Done():
		return herdrcore.Event{}, ctx.Err()
	}
}

func (e *fakeEvents) Close() error {
	e.once.Do(func() { e.closed = make(chan struct{}) })
	select {
	case <-e.closed:
	default:
		close(e.closed)
	}
	return nil
}

// useBackend sets the terminalBackend setting for one test.
func useBackend(t *testing.T, name string) {
	t.Helper()
	cfg().Set(terminalBackendKey, name)
	t.Cleanup(func() { cfg().Delete(terminalBackendKey) })
}

// nativeHarness drives a messaging host over native-messaging framing, as
// Chrome does, with the host's WebSocket server for its attach URLs.
type nativeHarness struct {
	sessions *SessionManager
	in       *io.PipeWriter
	out      *io.PipeReader
	mu       sync.Mutex
	next     int
	// notes holds the notifications read while waiting for answers.
	notes []jsonrpc.JSONRPCRequest
}

func newNativeHarness(t *testing.T, durable terminalBackend) *nativeHarness {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ln, err := listenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	m := NewSessionManager(ln.Addr().(*net.TCPAddr).Port, logger)
	m.durable = durable
	server := &http.Server{Handler: NewWebSocketHandler(m)}
	go server.Serve(ln)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	host := NewMessagingHost(logger, m, "test")
	host.SetInput(inR)
	host.SetOutput(outW)
	m.SetExtensionNotifier(nativeNotifier(host))
	go host.Listen()
	t.Cleanup(func() {
		inW.Close()
		outR.Close()
		server.Close()
		m.Close()
		for _, id := range m.List() {
			m.Destroy(id)
		}
	})
	return &nativeHarness{sessions: m, in: inW, out: outR}
}

// call sends one request and returns its result, or the error message.
func (h *nativeHarness) call(t *testing.T, method string, params any) (json.RawMessage, string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	id := fmt.Sprint(h.next)
	raw, _ := json.Marshal(params)
	req, _ := json.Marshal(jsonrpc.JSONRPCRequest{JSONRPCVersion: "2.0", ID: id, Method: method, Params: raw})
	if err := binary.Write(h.in, binary.LittleEndian, uint32(len(req))); err != nil {
		t.Fatal(err)
	}
	if _, err := h.in.Write(req); err != nil {
		t.Fatal(err)
	}
	for {
		var n uint32
		if err := binary.Read(h.out, binary.LittleEndian, &n); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(h.out, buf); err != nil {
			t.Fatal(err)
		}
		var note jsonrpc.JSONRPCRequest
		if json.Unmarshal(buf, &note) == nil && note.Method != "" && note.ID == "" {
			h.notes = append(h.notes, note)
			continue
		}
		var resp jsonrpc.JSONRPCResponse
		if err := json.Unmarshal(buf, &resp); err != nil {
			t.Fatal(err)
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			var e struct {
				Message string `json:"message"`
			}
			json.Unmarshal(resp.Error, &e)
			return nil, e.Message
		}
		return resp.Result, ""
	}
}

type endpointResult struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Pane string `json:"pane"`
}

func (h *nativeHarness) create(t *testing.T, params map[string]any) endpointResult {
	t.Helper()
	raw, msg := h.call(t, "tty.create", params)
	if msg != "" {
		t.Fatalf("tty.create: %s", msg)
	}
	var ep endpointResult
	if err := json.Unmarshal(raw, &ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

func (h *nativeHarness) list(t *testing.T) []string {
	t.Helper()
	raw, msg := h.call(t, "tty.list", nil)
	if msg != "" {
		t.Fatalf("tty.list: %s", msg)
	}
	var r struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	json.Unmarshal(raw, &r)
	var ids []string
	for _, s := range r.Sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// listFacts is tty.list's answer.
func (h *nativeHarness) listFacts(t *testing.T) []ttyListItem {
	t.Helper()
	raw, msg := h.call(t, "tty.list", nil)
	if msg != "" {
		t.Fatalf("tty.list: %s", msg)
	}
	var r struct {
		Sessions []ttyListItem `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r.Sessions
}

// events returns the tty.event notifications read so far, reading on (with a
// tty.list round trip) until there are at least n.
func (h *nativeHarness) events(t *testing.T, n int) []ttyEvent {
	t.Helper()
	var out []ttyEvent
	eventually(t, fmt.Sprintf("%d tty.event notifications", n), func() bool {
		h.listFacts(t)
		h.mu.Lock()
		defer h.mu.Unlock()
		out = out[:0]
		for _, note := range h.notes {
			if note.Method != "tty.event" {
				continue
			}
			var e ttyEvent
			if err := json.Unmarshal(note.Params, &e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return len(out) >= n
	})
	return out
}

func dialAttach(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	header := http.Header{}
	header.Set("Origin", extOrigin)
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readFrame(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage {
		t.Fatalf("frame kind %d", kind)
	}
	return string(data)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBuiltinBackendIsTheDefault(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	s, ok := h.sessions.Get(ep.ID)
	if !ok {
		t.Fatal("session not found")
	}
	if _, ok := s.term.(*ptyTerminal); !ok {
		t.Fatalf("default backend made %T", s.term)
	}
	if len(core.runs) != 0 {
		t.Fatal("the core ran a terminal with the preview off")
	}
}

func TestDurableBackendMapsTTYCalls(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})

	// tty.create runs a core terminal with the session's command, size,
	// environment and labels, and answers with the host's own ids.
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 100, "rows": 30})
	if strings.HasPrefix(ep.ID, "t") && len(ep.ID) < 4 || ep.Pane == "" || !strings.Contains(ep.URL, "/tty/"+ep.ID+"?token=") {
		t.Fatalf("endpoint %+v", ep)
	}
	if len(core.runs) != 1 {
		t.Fatalf("runs %d", len(core.runs))
	}
	run := core.runs[0]
	if run.Size == nil || run.Size.Cols != 100 || run.Size.Rows != 30 || !run.NoParent || len(run.Argv) == 0 {
		t.Fatalf("run %+v", run)
	}
	if run.Env[envSessionID] != publicID("session", ep.ID) || run.Env[envTabID] != publicID("tab", ep.Pane) {
		t.Fatalf("env %v", run.Env)
	}
	if run.Labels[coreSessionLabel] != publicID("session", ep.ID) || run.Labels[corePaneLabel] != publicID("tab", ep.Pane) {
		t.Fatalf("labels %v", run.Labels)
	}
	for _, v := range run.Labels {
		if v == ep.Pane || v == ep.ID {
			t.Fatal("a secret host id is in the core's labels")
		}
	}
	// No inherited descriptor: the page socket is the way to the page.
	if _, ok := run.Env[pageFDEnv]; ok {
		t.Fatalf("%s set for a core terminal", pageFDEnv)
	}
	if !strings.HasPrefix(run.Env[envSocket], pageSocketDir+"/") {
		t.Fatalf("socket %q", run.Env[envSocket])
	}
	for k := range run.Env {
		if strings.HasPrefix(k, "HC_") {
			t.Fatalf("%s passed over the core's own", k)
		}
	}

	if ids := h.list(t); len(ids) != 1 || ids[0] != ep.ID {
		t.Fatalf("tty.list %v", ids)
	}

	// The WebSocket gets the core's first paint as its one replay frame,
	// then live output; its input goes to the core stream.
	conn := dialAttach(t, ep.URL)
	if got := readFrame(t, conn); got != "paint of t1" {
		t.Fatalf("replay %q", got)
	}
	if a := core.attaches[0]; a.Terminal != "t1" || a.Cols != 100 || a.Rows != 30 || a.History != coreHistoryLines {
		t.Fatalf("attach %+v", a)
	}
	stream := core.stream(0)
	stream.out <- []byte("live")
	if got := readFrame(t, conn); got != "live" {
		t.Fatalf("live %q", got)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "input", func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return len(stream.input) == 1 && stream.input[0] == "ls\r"
	})

	// tty.resize goes to the core through the viewer's stream.
	if _, msg := h.call(t, "tty.resize", map[string]any{"tty": ep.ID, "cols": 120, "rows": 40}); msg != "" {
		t.Fatal(msg)
	}
	stream.mu.Lock()
	resizes := append([][2]int(nil), stream.resizes...)
	stream.mu.Unlock()
	if len(resizes) != 1 || resizes[0] != [2]int{120, 40} {
		t.Fatalf("resizes %v", resizes)
	}

	// tty.attach hands out a new token; the new viewer replaces the old one
	// at the new size.
	raw, msg := h.call(t, "tty.attach", map[string]any{"id": ep.ID})
	if msg != "" {
		t.Fatal(msg)
	}
	var again endpointResult
	json.Unmarshal(raw, &again)
	conn2 := dialAttach(t, again.URL)
	if got := readFrame(t, conn2); got != "paint of t1" {
		t.Fatalf("second replay %q", got)
	}
	if a := core.attaches[1]; a.Cols != 120 || a.Rows != 40 {
		t.Fatalf("reattach %+v", a)
	}
	eventually(t, "the first stream to close", stream.isClosed)

	// tty.destroy kills and removes the core terminal and revokes the
	// pane tab's grants.
	if _, msg := h.call(t, "tty.destroy", map[string]any{"id": ep.ID, "pane": ep.Pane}); msg != "" {
		t.Fatal(msg)
	}
	if len(core.killed) != 1 || core.killed[0] != "t1" || len(core.removed) != 1 || core.removed[0] != "t1" {
		t.Fatalf("killed %v removed %v", core.killed, core.removed)
	}
	if ids := h.list(t); len(ids) != 0 {
		t.Fatalf("tty.list after destroy %v", ids)
	}
	if _, err := os.Stat(run.Env[envSocket]); !os.IsNotExist(err) {
		t.Fatalf("page socket outlived the session: %v", err)
	}
}

func TestDurableSessionIsReapedWhenItsProgramEnds(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	core.finish(core.runs[0].Labels[coreSessionLabel], "t1")
	eventually(t, "the session to be reaped", func() bool { return len(h.sessions.List()) == 0 })
	if _, msg := h.call(t, "tty.attach", map[string]any{"id": ep.ID}); msg == "" {
		t.Fatal("attached to an ended session")
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.removed) != 1 {
		t.Fatalf("finished terminal not removed: %v", core.removed)
	}
}

func TestDurableFallsBackToBuiltinWithANotice(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	useBackend(t, terminalBackendDurable)
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return nil, errCoreMissing }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	s, ok := h.sessions.Get(ep.ID)
	if !ok {
		t.Fatal("no session")
	}
	if _, ok := s.term.(*ptyTerminal); !ok {
		t.Fatalf("fallback made %T", s.term)
	}
	replay := readFrame(t, dialAttach(t, ep.URL))
	if !strings.Contains(replay, "Durable (preview) terminal backend could not start") || !strings.Contains(replay, errCoreMissing.Error()) {
		t.Fatalf("replay %q", replay)
	}
}

func TestDurableStartFailureLeavesNothingBehind(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.runErr = &herdrcore.Error{Message: "spawn_failed", Kind: "spawn_failed"}
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	before, _ := os.ReadDir(pageSocketDir)
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	if ids := h.list(t); len(ids) != 1 || ids[0] != ep.ID {
		t.Fatalf("tty.list %v", ids)
	}
	// One socket for the Built-in session; the failed Durable one's is gone.
	after, _ := os.ReadDir(pageSocketDir)
	if len(after) != len(before)+1 {
		t.Fatalf("page sockets %d, then %d", len(before), len(after))
	}
}

func TestDurableSessionOutlivesALostSubscription(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	core.dropEvents()
	eventually(t, "the terminal to be watched again", func() bool { return core.subscriptions() == 2 })
	if ids := h.list(t); len(ids) != 1 || ids[0] != ep.ID {
		t.Fatalf("tty.list after a lost subscription %v", ids)
	}
	core.mu.Lock()
	killed := len(core.killed)
	core.mu.Unlock()
	if killed != 0 {
		t.Fatal("a lost subscription killed a live terminal")
	}
	// The new subscription still sees the program end.
	core.finish(core.runs[0].Labels[coreSessionLabel], "t1")
	eventually(t, "the session to be reaped", func() bool { return len(h.sessions.List()) == 0 })
}

func TestDurableStatusAndTitleReachTheExtension(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	session := core.runs[0].Labels[coreSessionLabel]

	if items := h.listFacts(t); len(items) != 1 || items[0].ID != ep.ID || items[0].Status != nil {
		t.Fatalf("tty.list before any report %+v", items)
	}
	core.send(session, herdrcore.Event{Seq: 1, Type: "status", Terminal: "t1", Data: json.RawMessage(`{"reported":true,"status":{"state":"working","msg":"running tests"}}`)})
	// Another terminal's report is not this session's.
	core.send(session, herdrcore.Event{Seq: 2, Type: "status", Terminal: "t9", Data: json.RawMessage(`{"status":{"state":"error"}}`)})
	core.send(session, herdrcore.Event{Seq: 3, Type: "title", Terminal: "t1", Data: json.RawMessage(`{"title":"pi"}`)})
	core.send(session, herdrcore.Event{Seq: 4, Type: "status", Terminal: "t1", Data: json.RawMessage(`{"status":{"state":"blocked","kind":"permission"}}`)})
	core.send(session, herdrcore.Event{Seq: 5, Type: "status", Terminal: "t1", Data: json.RawMessage(`{"status":null}`)})

	events := h.events(t, 4)
	want := []ttyEvent{
		{TTY: ep.ID, Status: &paneStatus{State: "working", Msg: "running tests"}},
		{TTY: ep.ID, Status: &paneStatus{State: "working", Msg: "running tests"}, Title: "pi"},
		{TTY: ep.ID, Status: &paneStatus{State: "blocked", Kind: "permission"}, Title: "pi"},
		{TTY: ep.ID, Title: "pi"},
	}
	if len(events) != len(want) {
		t.Fatalf("events %+v", events)
	}
	for i := range want {
		if events[i].TTY != want[i].TTY || events[i].Title != want[i].Title || !sameStatus(events[i].Status, want[i].Status) {
			t.Fatalf("event %d = %+v %+v; want %+v %+v", i, events[i], events[i].Status, want[i], want[i].Status)
		}
	}

	// A panel that opens later learns the current facts from tty.list.
	core.send(session, herdrcore.Event{Seq: 6, Type: "status", Terminal: "t1", Data: json.RawMessage(`{"status":{"state":"done"}}`)})
	h.events(t, 5)
	items := h.listFacts(t)
	if len(items) != 1 || items[0].Title != "pi" || !sameStatus(items[0].Status, &paneStatus{State: "done"}) {
		t.Fatalf("tty.list %+v", items)
	}
}

func TestDurableStatusComesFromTheSnapshotAfterMissedEvents(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	core.send(core.runs[0].Labels[coreSessionLabel], herdrcore.Event{Seq: 9, Type: "missed", Terminals: []herdrcore.Terminal{
		{Address: "t1", State: "live", Title: "codex", Status: &herdrcore.Status{State: "blocked", Msg: "allow?", Kind: "question"}},
	}})
	events := h.events(t, 1)
	if e := events[0]; e.TTY != ep.ID || e.Title != "codex" || !sameStatus(e.Status, &paneStatus{State: "blocked", Msg: "allow?", Kind: "question"}) {
		t.Fatalf("event %+v %+v", e, e.Status)
	}
}

func TestBuiltinSessionsReportNoStatus(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	h := newNativeHarness(t, nil)
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	items := h.listFacts(t)
	if len(items) != 1 || items[0].ID != ep.ID || items[0].Status != nil || items[0].Title != "" {
		t.Fatalf("tty.list %+v", items)
	}
}

func TestDurableUnansweredRunDoesNotStartTwice(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.runLost = true
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	if _, msg := h.call(t, "tty.create", map[string]any{"mode": "shell", "cols": 80, "rows": 24}); !strings.Contains(msg, errCoreUnsure.Error()) {
		t.Fatalf("tty.create error %q", msg)
	}
	if ids := h.list(t); len(ids) != 0 {
		t.Fatalf("started in Built-in too: %v", ids)
	}
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.killed) != 1 || core.killed[0] != "t1" || len(core.removed) != 1 {
		t.Fatalf("killed %v removed %v", core.killed, core.removed)
	}
}

func TestDurableUnsureStartIsKeptUntilItsCleanupWorks(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.runLost = true
	core.listErr = context.DeadlineExceeded
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	if _, msg := h.call(t, "tty.create", map[string]any{"mode": "shell", "cols": 80, "rows": 24}); !strings.Contains(msg, errCoreUnsure.Error()) {
		t.Fatalf("tty.create error %q", msg)
	}
	// While the first program may still run, nothing starts in its place.
	core.mu.Lock()
	core.runLost = false
	core.mu.Unlock()
	if _, msg := h.call(t, "tty.create", map[string]any{"mode": "shell", "cols": 80, "rows": 24}); !strings.Contains(msg, errCoreUnsure.Error()) {
		t.Fatalf("tty.create while unsure: error %q", msg)
	}
	if ids := h.list(t); len(ids) != 0 {
		t.Fatalf("started while unsure: %v", ids)
	}
	core.mu.Lock()
	runs := len(core.runs)
	core.listErr = nil
	core.mu.Unlock()
	if runs != 1 {
		t.Fatalf("runs %d", runs)
	}
	// Once the core answers, the first program is ended and starts resume.
	eventually(t, "the unsure terminal to be ended", func() bool {
		core.mu.Lock()
		defer core.mu.Unlock()
		return len(core.killed) == 1 && core.killed[0] == "t1" && len(core.removed) == 1
	})
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	if ids := h.list(t); len(ids) != 1 || ids[0] != ep.ID {
		t.Fatalf("tty.list %v", ids)
	}
}

func TestCloseEndsAnUnsureStart(t *testing.T) {
	core := newFakeCore()
	b := &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }}
	h := newNativeHarness(t, b)
	core.mu.Lock()
	core.runs = append(core.runs, herdrcore.RunParams{Labels: map[string]string{coreSessionLabel: "s1"}})
	core.mu.Unlock()
	b.unsureMu.Lock()
	b.unsure = map[string]struct{}{"s1": {}}
	b.unsureMu.Unlock()
	h.sessions.Close()
	core.mu.Lock()
	defer core.mu.Unlock()
	if len(core.killed) != 1 || core.killed[0] != "t1" || len(core.removed) != 1 {
		t.Fatalf("killed %v removed %v", core.killed, core.removed)
	}
}

func TestSettleTriesEveryUnsureStart(t *testing.T) {
	core := newFakeCore()
	b := &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }}
	core.mu.Lock()
	for _, session := range []string{"s1", "s2", "s3"} {
		core.runs = append(core.runs, herdrcore.RunParams{Labels: map[string]string{coreSessionLabel: session}})
	}
	core.killErr = map[string]error{"t2": context.DeadlineExceeded}
	core.mu.Unlock()
	b.unsureMu.Lock()
	b.unsure = map[string]struct{}{"s1": {}, "s2": {}, "s3": {}}
	b.unsureMu.Unlock()
	if err := b.settle(); !errors.Is(err, errCoreUnsure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("settle error %v", err)
	}
	core.mu.Lock()
	killed := append([]string(nil), core.killed...)
	core.mu.Unlock()
	slices.Sort(killed)
	if !slices.Equal(killed, []string{"t1", "t3"}) {
		t.Fatalf("killed %v", killed)
	}
	b.unsureMu.Lock()
	defer b.unsureMu.Unlock()
	if _, kept := b.unsure["s2"]; len(b.unsure) != 1 || !kept {
		t.Fatalf("still unsure %v", b.unsure)
	}
}

func TestCloseWaitsForADurableStart(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.runGate = make(chan struct{})
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	created := make(chan error, 1)
	go func() {
		command := newTerminalCommand("sh")
		command.Cols, command.Rows = 80, 24
		_, err := h.sessions.Create("", "", "", command)
		created <- err
	}()
	eventually(t, "the session to register", func() bool {
		h.sessions.mu.RLock()
		defer h.sessions.mu.RUnlock()
		return len(h.sessions.sessions) == 1
	})
	closed := make(chan struct{})
	go func() {
		h.sessions.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a session was starting")
	case <-time.After(50 * time.Millisecond):
	}
	close(core.runGate)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	<-closed
	core.mu.Lock()
	removed := append([]string(nil), core.removed...)
	core.mu.Unlock()
	if len(h.sessions.List()) != 0 || len(removed) != 1 || removed[0] != "t1" {
		t.Fatalf("sessions %v removed %v", h.sessions.List(), removed)
	}
	command := newTerminalCommand("sh")
	if _, err := h.sessions.Create("", "", "", command); err == nil {
		t.Fatal("Create after Close started a session")
	}
}

func TestCloseEndsDurableSessions(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	h.sessions.Close()
	if len(h.sessions.List()) != 0 || len(core.removed) != 1 {
		t.Fatalf("sessions %v removed %v", h.sessions.List(), core.removed)
	}
}

func TestCoreBackendRedialsADroppedConnection(t *testing.T) {
	first, second := newFakeCore(), newFakeCore()
	dials := 0
	b := &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) {
		dials++
		if dials == 1 {
			return first, nil
		}
		return second, nil
	}}
	ctx := context.Background()
	if c, _ := b.connect(ctx); c != first {
		t.Fatal("first dial")
	}
	if c, _ := b.connect(ctx); c != first || dials != 1 {
		t.Fatal("an open connection was redialled")
	}
	close(first.done)
	if c, _ := b.connect(ctx); c != second || dials != 2 {
		t.Fatal("a dropped connection was not redialled")
	}
}

func TestCoreEnvKeepsTheCoresOwnKeys(t *testing.T) {
	env := coreEnv([]string{"HOME=/home/me", "HC_SESSION=mine", "HC_SOCKET=/x", "PATH=/bin", "XDG_STATE_HOME=/state"})
	if _, ok := env["HC_SESSION"]; ok {
		t.Fatal("HC_SESSION passed on")
	}
	if env["PATH"] != "/bin" || env["XDG_STATE_HOME"] != "/state" || env["XDG_CONFIG_HOME"] != "/home/me/.config" {
		t.Fatalf("env %v", env)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestDurableBackendRealCore runs a pane tab through the pinned core when
// AGENT_PANE_TEST_HC names it, the way the side panel does: tty.create,
// WebSocket attach, typing, resize, a page read over the page socket, and
// tty.destroy.
func TestDurableBackendRealCore(t *testing.T) {
	binary := os.Getenv("AGENT_PANE_TEST_HC")
	if binary == "" {
		t.Skip("set AGENT_PANE_TEST_HC to the pinned hc to run against a real core")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.MkdirTemp("", "apcore")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	core := herdrcore.Private(data)
	core.Binary = binary
	t.Cleanup(func() {
		cmd := exec.Command(core.Binary, "server", "stop")
		cmd.Env = core.Env(os.Environ())
		cmd.Run()
	})
	useBackend(t, terminalBackendDurable)
	cfg().Set("command", "/bin/sh")
	t.Cleanup(func() { cfg().Delete("command") })

	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all", content: "from the tab"}
	h := newNativeHarness(t, newCoreBackend(discardLogger(), core))
	h.sessions.SetExtensionCaller(ext.call)
	ep := h.create(t, map[string]any{"mode": "shell", "cols": 80, "rows": 24})
	s, ok := h.sessions.Get(ep.ID)
	if !ok {
		t.Fatal("no session")
	}
	if _, ok := s.term.(*coreTerminal); !ok {
		t.Fatalf("session runs in %T", s.term)
	}

	conn := dialAttach(t, ep.URL)
	readFrame(t, conn)
	var out strings.Builder
	waitWS := func(want string) {
		t.Helper()
		for !strings.Contains(out.String(), want) {
			out.WriteString(readFrame(t, conn))
		}
	}
	type_ := func(line string) {
		t.Helper()
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte(line+"\r")); err != nil {
			t.Fatal(err)
		}
	}
	type_(`echo "SOCK[$AGENT_PANE_SOCKET] FD[$AGENT_PANE_FD] TAB[$AGENT_PANE_TAB_ID]"`)
	waitWS("SOCK[" + pageSocketDir)
	if !strings.Contains(out.String(), "FD[]") || !strings.Contains(out.String(), "TAB["+publicID("tab", ep.Pane)+"]") {
		t.Fatalf("environment %q", out.String())
	}

	if _, msg := h.call(t, "tty.resize", map[string]any{"tty": ep.ID, "cols": 101, "rows": 33}); msg != "" {
		t.Fatal(msg)
	}
	type_(`stty size`)
	waitWS("33 101")

	// A page read from the terminal's process goes through the page socket,
	// checked against the pid the core reported, and the approval prompt.
	type_(fmt.Sprintf(`PAGE_SOCKET_HELPER=1 %q -test.run='^TestPageSocketHelperProcess$'`, os.Args[0]))
	waitWS("OK[from the tab]")
	if ext.promptCount() != 1 {
		t.Fatalf("prompts %d", ext.promptCount())
	}

	// agent-pane status in the terminal reaches the extension as tty.event.
	type_(fmt.Sprintf(`STATUS_HELPER=1 %q -test.run='^TestStatusHelperProcess$'`, os.Args[0]))
	eventually(t, "the blocked status", func() bool {
		events := h.events(t, 0)
		return len(events) > 0 && sameStatus(events[len(events)-1].Status, &paneStatus{State: "blocked", Msg: "allow writing?", Kind: "permission"})
	})
	if items := h.listFacts(t); len(items) != 1 || items[0].Status == nil || items[0].Status.State != "blocked" {
		t.Fatalf("tty.list %+v", items)
	}

	if _, msg := h.call(t, "tty.destroy", map[string]any{"id": ep.ID, "pane": ep.Pane}); msg != "" {
		t.Fatal(msg)
	}
	c, err := core.Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	left, err := c.List(context.Background(), nil, true)
	if err != nil || len(left) != 0 {
		t.Fatalf("core terminals after destroy: %+v %v", left, err)
	}
}

// TestStatusHelperProcess reports a status the way an agent's command tool
// runs agent-pane status: with stdout captured, not the terminal.
func TestStatusHelperProcess(t *testing.T) {
	if os.Getenv("STATUS_HELPER") == "" {
		t.Skip()
	}
	cmd := NewCmdStatus()
	cmd.SetArgs([]string{"blocked", "--kind", "permission", "allow", "writing?"})
	cmd.SetOut(io.Discard)
	if err := cmd.Execute(); err != nil {
		fmt.Printf("ERR[%s]\n", err)
	}
	os.Exit(0)
}
