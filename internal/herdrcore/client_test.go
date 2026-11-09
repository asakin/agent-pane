package herdrcore

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeCore is the core's side of a connection, speaking the wire format the
// pinned hc 0.1.19 speaks.
type fakeCore struct {
	t      *testing.T
	conn   net.Conn
	r      *bufio.Reader
	frames chan frame // what the client sent, read as it arrives
}

type frame struct {
	ch      uint32
	kind    byte
	payload []byte
}

type request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func helloReply(generation int) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 0, "result": map[string]any{
		"generation": generation, "version": "0.1.19", "core": map[string]any{"id": "c1", "name": "box"},
		"framed": true, "methods": []string{"hello"}, "features": []string{"framed", "attach"},
	}})
	return string(b)
}

// startFake connects a client to a fake core that answers hello with reply,
// returning both once the handshake is done. The hello request is passed to
// gotHello.
func startFake(t *testing.T, reply string, gotHello func(map[string]any)) (*Client, *fakeCore, error) {
	t.Helper()
	return startFakeBuffered(t, reply, gotHello, 64)
}

// startFakeBuffered is startFake with room for buffered frames the test has
// not read. Past that, the client's writes block, as on a socket whose
// buffer is full.
func startFakeBuffered(t *testing.T, reply string, gotHello func(map[string]any), buffered int) (*Client, *fakeCore, error) {
	t.Helper()
	clientConn, coreConn := net.Pipe()
	core := &fakeCore{t: t, conn: coreConn, r: bufio.NewReader(coreConn), frames: make(chan frame, buffered)}
	go func() {
		line, err := core.r.ReadBytes('\n')
		if err != nil {
			return
		}
		var hello map[string]any
		json.Unmarshal(line, &hello)
		if gotHello != nil {
			gotHello(hello)
		}
		coreConn.Write([]byte(reply + "\n"))
		core.readFrames()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := NewClient(ctx, clientConn)
	if err != nil {
		coreConn.Close()
		return nil, nil, err
	}
	t.Cleanup(func() {
		coreConn.Close()
		c.Close()
	})
	return c, core, nil
}

// newUnbufferedFake returns a client whose writes block once the core holds
// one frame the test has not read.
func newUnbufferedFake(t *testing.T) (*Client, *fakeCore) {
	t.Helper()
	c, core, err := startFakeBuffered(t, helloReply(Generation), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c, core
}

// stallWrites leaves the client's writer blocked on the socket: the core
// holds one Get it has not handed to the test, and the writer is sending a
// second. The test reads both requests back later.
func stallWrites(c *Client) {
	go c.Get(context.Background(), "stall1")
	time.Sleep(20 * time.Millisecond)
	go c.Get(context.Background(), "stall2")
	time.Sleep(20 * time.Millisecond)
}

func pendingCalls(c *Client) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func newFake(t *testing.T) (*Client, *fakeCore) {
	t.Helper()
	c, core, err := startFake(t, helloReply(Generation), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, core
}

// readFrames reads the client's frames into f.frames, so a client write
// never blocks on the test, as on a socket with a kernel buffer.
func (f *fakeCore) readFrames() {
	defer close(f.frames)
	h := make([]byte, frameHeader)
	for {
		if _, err := io.ReadFull(f.r, h); err != nil {
			return
		}
		p := make([]byte, binary.LittleEndian.Uint32(h[5:9]))
		if _, err := io.ReadFull(f.r, p); err != nil {
			return
		}
		f.frames <- frame{binary.LittleEndian.Uint32(h[0:4]), h[4], p}
	}
}

func (f *fakeCore) read() frame {
	f.t.Helper()
	select {
	case fr, ok := <-f.frames:
		if !ok {
			f.t.Fatal("fake core: connection closed")
		}
		return fr
	case <-time.After(5 * time.Second):
		f.t.Fatal("fake core: no frame")
	}
	return frame{}
}

func (f *fakeCore) readRequest() request {
	f.t.Helper()
	fr := f.read()
	if fr.ch != 0 || fr.kind != frameData {
		f.t.Fatalf("fake core: got frame ch %d kind %d, want a request", fr.ch, fr.kind)
	}
	var req request
	if err := json.Unmarshal(fr.payload, &req); err != nil {
		f.t.Fatal(err)
	}
	return req
}

func (f *fakeCore) write(ch uint32, kind byte, p []byte) {
	f.t.Helper()
	buf := make([]byte, frameHeader+len(p))
	binary.LittleEndian.PutUint32(buf[0:4], ch)
	buf[4] = kind
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(p)))
	copy(buf[frameHeader:], p)
	f.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := f.conn.Write(buf); err != nil {
		f.t.Fatalf("fake core: write: %v", err)
	}
}

func (f *fakeCore) reply(id uint64, result any) {
	f.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	f.write(0, frameData, b)
}

func (f *fakeCore) replyError(id uint64, kind, message string) {
	f.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
		"code": -32000, "message": message, "data": map[string]any{"kind": kind, "hint": "see `hc ls --all`"},
	}})
	f.write(0, frameData, b)
}

func terminalFacts(address string) map[string]any {
	return map[string]any{
		"address": address, "argv": []string{"sh"}, "cwd": "/tmp", "state": "live",
		"labels": map[string]string{"agent-pane.pane": "p1"}, "cols": 80, "rows": 24,
		"created_ms": 1, "screen": "exact", "pid": 42,
	}
}

func TestHandshakeSendsGenerationAndFramed(t *testing.T) {
	var hello map[string]any
	c, _, err := startFake(t, helloReply(Generation), func(h map[string]any) { hello = h })
	if err != nil {
		t.Fatal(err)
	}
	params, _ := hello["params"].(map[string]any)
	if hello["method"] != "hello" || params["generation"] != float64(Generation) || params["framed"] != true {
		t.Fatalf("hello request = %v", hello)
	}
	if c.Hello.Version != "0.1.19" || c.Hello.Generation != Generation || c.Hello.Core.ID != "c1" {
		t.Fatalf("Hello = %+v", c.Hello)
	}
}

func TestHandshakeRejectsGenerationTheCoreRefuses(t *testing.T) {
	refusal := `{"error":{"code":-32000,"data":{"hint":"this core speaks generation 2","kind":"unsupported_generation"},"message":"protocol generation 1 not supported"},"id":0,"jsonrpc":"2.0"}`
	_, _, err := startFake(t, refusal, nil)
	if !errors.Is(err, ErrUnsupportedGeneration) {
		t.Fatalf("err = %v, want ErrUnsupportedGeneration", err)
	}
	if !IsKind(err, "unsupported_generation") || !strings.Contains(err.Error(), "this core speaks generation 2") {
		t.Fatalf("err = %q, want the core's kind and hint", err)
	}
}

func TestHandshakeRejectsOtherGenerationInReply(t *testing.T) {
	_, _, err := startFake(t, helloReply(Generation+1), nil)
	if !errors.Is(err, ErrUnsupportedGeneration) {
		t.Fatalf("err = %v, want ErrUnsupportedGeneration", err)
	}
	want := "herdr-core 0.1.19 speaks generation 2, Agent Pane speaks 1"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to contain %q", err, want)
	}
}

func TestHandshakeRejectsUnframedReply(t *testing.T) {
	reply := strings.Replace(helloReply(Generation), `"framed":true`, `"framed":false`, 1)
	if _, _, err := startFake(t, reply, nil); err == nil || !strings.Contains(err.Error(), "framed") {
		t.Fatalf("err = %v, want a framed-mode error", err)
	}
}

func TestHandshakeHonorsContext(t *testing.T) {
	clientConn, coreConn := net.Pipe()
	defer coreConn.Close()
	go io.Copy(io.Discard, coreConn) // reads hello, never answers
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewClient(ctx, clientConn); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestTerminalCalls(t *testing.T) {
	c, core := newFake(t)
	ctx := context.Background()

	type result struct {
		name string
		err  error
		got  any
	}
	results := make(chan result, 1)

	go func() {
		term, err := c.Run(ctx, RunParams{
			Argv:   []string{"sh", "-l"},
			Cwd:    "/work",
			Env:    map[string]string{"AGENT_PANE": "1"},
			Labels: map[string]string{"agent-pane.pane": "p1"},
			Size:   &Size{Cols: 100, Rows: 30},
			Keep:   true,
		})
		results <- result{"run", err, term}
	}()
	req := core.readRequest()
	if req.Method != "terminal.run" {
		t.Fatalf("method = %q", req.Method)
	}
	var run map[string]any
	json.Unmarshal(req.Params, &run)
	if run["cwd"] != "/work" || run["keep"] != true || run["size"].(map[string]any)["cols"] != float64(100) ||
		run["labels"].(map[string]any)["agent-pane.pane"] != "p1" || run["env"].(map[string]any)["AGENT_PANE"] != "1" {
		t.Fatalf("terminal.run params = %s", req.Params)
	}
	core.reply(req.ID, terminalFacts("t7"))
	r := <-results
	if term := r.got.(Terminal); r.err != nil || term.Address != "t7" || *term.Pid != 42 || term.Labels["agent-pane.pane"] != "p1" {
		t.Fatalf("Run = %+v, %v", r.got, r.err)
	}

	go func() {
		ts, err := c.List(ctx, map[string]string{"agent-pane.pane": "p1"}, false)
		results <- result{"list", err, ts}
	}()
	req = core.readRequest()
	if req.Method != "terminal.list" || string(req.Params) != `{"all":false,"labels":{"agent-pane.pane":"p1"}}` {
		t.Fatalf("list request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, map[string]any{"terminals": []any{terminalFacts("t7"), terminalFacts("t8")}, "hidden_finished": 0, "machines": []any{}})
	if r := <-results; r.err != nil || len(r.got.([]Terminal)) != 2 {
		t.Fatalf("List = %+v, %v", r.got, r.err)
	}

	go func() {
		_, err := c.Get(ctx, "t9")
		results <- result{"get", err, nil}
	}()
	req = core.readRequest()
	core.replyError(req.ID, "not_found", `no terminal "t9"`)
	r = <-results
	var coreErr *Error
	if !errors.As(r.err, &coreErr) || coreErr.Kind != "not_found" || !IsKind(r.err, "not_found") {
		t.Fatalf("Get err = %v, want a not_found *Error", r.err)
	}

	go func() {
		term, err := c.Kill(ctx, "t7")
		results <- result{"kill", err, term}
	}()
	req = core.readRequest()
	if req.Method != "terminal.kill" || string(req.Params) != `{"terminal":"t7"}` {
		t.Fatalf("kill request = %s %s", req.Method, req.Params)
	}
	facts := terminalFacts("t7")
	facts["state"] = "finished"
	core.reply(req.ID, facts)
	if r := <-results; r.err != nil || r.got.(Terminal).State != "finished" {
		t.Fatalf("Kill = %+v, %v", r.got, r.err)
	}

	go func() {
		term, err := c.Label(ctx, "t7", map[string]string{"agent-pane.window": ""})
		results <- result{"label", err, term}
	}()
	req = core.readRequest()
	if req.Method != "terminal.label" || string(req.Params) != `{"labels":{"agent-pane.window":""},"terminal":"t7"}` {
		t.Fatalf("label request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, terminalFacts("t7"))
	if r := <-results; r.err != nil || r.got.(Terminal).Address != "t7" {
		t.Fatalf("Label = %+v, %v", r.got, r.err)
	}

	go func() { results <- result{"rm", c.Remove(ctx, "t7"), nil} }()
	req = core.readRequest()
	if req.Method != "terminal.rm" {
		t.Fatalf("method = %q", req.Method)
	}
	core.reply(req.ID, map[string]any{"removed": "t7"})
	if r := <-results; r.err != nil {
		t.Fatal(r.err)
	}
}

func TestConcurrentCallsMatchRepliesByID(t *testing.T) {
	c, core := newFake(t)
	errs := make(chan error, 2)
	addrs := make(chan string, 2)
	for range 2 {
		go func() {
			term, err := c.Get(context.Background(), "x")
			errs <- err
			addrs <- term.Address
		}()
	}
	first, second := core.readRequest(), core.readRequest()
	// Answer out of order.
	core.reply(second.ID, terminalFacts("second"))
	core.reply(first.ID, terminalFacts("first"))
	got := map[string]bool{}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		got[<-addrs] = true
	}
	if !got["first"] || !got["second"] {
		t.Fatalf("got %v", got)
	}
}

// attach drives an Attach through the fake core and returns the attachment
// and its channel.
func attach(t *testing.T, c *Client, core *fakeCore, ch uint32) *Attachment {
	t.Helper()
	type result struct {
		a   *Attachment
		err error
	}
	done := make(chan result, 1)
	go func() {
		a, err := c.Attach(context.Background(), AttachParams{Terminal: "t7", Cols: 80, Rows: 24, History: 500})
		done <- result{a, err}
	}()
	req := core.readRequest()
	if req.Method != "terminal.attach" || string(req.Params) != `{"terminal":"t7","cols":80,"rows":24,"history":500}` {
		t.Fatalf("attach request = %s %s", req.Method, req.Params)
	}
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
		"channel": ch, "terminal": terminalFacts("t7"), "resize_notifications": false, "automatic_input": true,
	}})
	// The first paint follows the reply straight away, before the client
	// has returned from Attach; it must not be lost.
	core.write(0, frameData, b)
	core.write(ch, frameData, []byte("first paint"))
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	return r.a
}

func readN(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func TestAttachStreamsOutputAndGrantsCredit(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	if a.Terminal.Address != "t7" {
		t.Fatalf("Terminal = %+v", a.Terminal)
	}
	if got := readN(t, a, len("first paint")); got != "first paint" {
		t.Fatalf("read %q", got)
	}
	credit := core.read()
	if credit.ch != 3 || credit.kind != frameCredit || binary.LittleEndian.Uint32(credit.payload) != uint32(len("first paint")) {
		t.Fatalf("credit frame = %+v", credit)
	}

	// Another channel's data never reaches this stream.
	core.write(9, frameData, []byte("other"))
	core.write(3, frameData, []byte("live"))
	if got := readN(t, a, 4); got != "live" {
		t.Fatalf("read %q", got)
	}
	core.read() // credit
}

func TestAttachWriteSendsDataFrames(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	go a.Write([]byte("ls\r"))
	f := core.read()
	if f.ch != 3 || f.kind != frameData || string(f.payload) != "ls\r" {
		t.Fatalf("frame = %+v", f)
	}

	big := strings.Repeat("x", maxDataFrame+10)
	done := make(chan error, 1)
	go func() {
		_, err := a.Write([]byte(big))
		done <- err
	}()
	if f := core.read(); len(f.payload) != maxDataFrame {
		t.Fatalf("first frame has %d bytes, want %d", len(f.payload), maxDataFrame)
	}
	if f := core.read(); len(f.payload) != 10 {
		t.Fatalf("second frame has %d bytes, want 10", len(f.payload))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAttachResizeSendsResizeFrame(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	go a.Resize(100, 30)
	f := core.read()
	// hc 0.1.19 sends RESIZE as u16 cols then u16 rows, little-endian.
	if f.ch != 3 || f.kind != frameResize || string(f.payload) != "\x64\x00\x1e\x00" {
		t.Fatalf("frame = %+v", f)
	}
	for _, size := range [][2]int{{0, 24}, {80, 0}, {70000, 24}} {
		if err := a.Resize(size[0], size[1]); err == nil {
			t.Fatalf("Resize(%d, %d) succeeded", size[0], size[1])
		}
	}
}

func TestAttachRejectsInvalidSize(t *testing.T) {
	c, _ := newFake(t)
	if _, err := c.Attach(context.Background(), AttachParams{Terminal: "t7", Cols: 0, Rows: 24}); err == nil {
		t.Fatal("Attach with 0 columns succeeded")
	}
}

func TestAttachCoreCloseEndsStreamWithFacts(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	if _, ok := a.End(); ok {
		t.Fatal("End reported an open stream as ended")
	}
	readN(t, a, len("first paint"))
	core.read() // credit
	core.write(3, frameData, []byte("bye"))
	core.write(3, frameClose, []byte(`{"exit_code":null,"finished_ms":1794202582025,"reason":"killed","signal":1}`))

	// Output sent before the close is still read, then EOF.
	if got := readN(t, a, 3); got != "bye" {
		t.Fatalf("read %q", got)
	}
	if _, err := a.Read(make([]byte, 8)); err != io.EOF {
		t.Fatalf("Read err = %v, want io.EOF", err)
	}
	end, ok := a.End()
	if !ok || end.Reason != "killed" || end.Signal == nil || *end.Signal != 1 || end.ExitCode != nil {
		t.Fatalf("End = %+v, %v", end, ok)
	}
	if _, err := a.Write([]byte("x")); !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("Write after close err = %v, want ErrStreamEnded", err)
	}
	if err := a.Resize(80, 24); !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("Resize after close err = %v, want ErrStreamEnded", err)
	}
	// Closing a stream the core ended sends nothing.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	go c.Get(context.Background(), "t7")
	for {
		f := core.read()
		if f.ch == 0 {
			break
		}
		// Reading "bye" before the CLOSE is dispatched grants credit for it.
		if f.ch != 3 || f.kind != frameCredit {
			t.Fatalf("got frame %+v after Close, want the next request", f)
		}
	}
}

func TestAttachExitCode(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	readN(t, a, len("first paint"))
	core.read() // credit
	core.write(3, frameClose, []byte(`{"exit_code":3,"finished_ms":5,"reason":"exited","signal":null}`))
	if _, err := a.Read(make([]byte, 8)); err != io.EOF {
		t.Fatalf("Read err = %v, want io.EOF", err)
	}
	if end, ok := a.End(); !ok || end.Reason != "exited" || end.ExitCode == nil || *end.ExitCode != 3 {
		t.Fatalf("End = %+v, %v", end, ok)
	}
}

func TestAttachCloseSendsCloseAndUnblocksRead(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	readN(t, a, len("first paint"))
	core.read() // credit

	readErr := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 8))
		readErr <- err
	}()
	time.Sleep(10 * time.Millisecond)
	go a.Close()
	f := core.read()
	if f.ch != 3 || f.kind != frameClose || len(f.payload) != 0 {
		t.Fatalf("frame = %+v, want CLOSE on channel 3", f)
	}
	if err := <-readErr; !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Read err = %v, want ErrStreamClosed", err)
	}
	if _, err := a.Write([]byte("x")); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Write err = %v, want ErrStreamClosed", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Late data for the closed channel is dropped, not delivered or fatal.
	core.write(3, frameData, []byte("late"))
	go c.Get(context.Background(), "t7")
	core.readRequest()
}

func TestAttachErrorReply(t *testing.T) {
	c, core := newFake(t)
	done := make(chan error, 1)
	go func() {
		_, err := c.Attach(context.Background(), AttachParams{Terminal: "t7", Cols: 80, Rows: 24})
		done <- err
	}()
	req := core.readRequest()
	core.replyError(req.ID, "finished", "t7 has finished")
	if err := <-done; !IsKind(err, "finished") {
		t.Fatalf("err = %v, want kind finished", err)
	}
}

func TestAbandonedAttachClosesItsChannel(t *testing.T) {
	c, core := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Attach(ctx, AttachParams{Terminal: "t7", Cols: 80, Rows: 24})
		done <- err
	}()
	req := core.readRequest()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	core.reply(req.ID, map[string]any{"channel": 4, "terminal": terminalFacts("t7"), "resize_notifications": false, "automatic_input": true})
	if f := core.read(); f.ch != 4 || f.kind != frameClose {
		t.Fatalf("frame = %+v, want CLOSE on channel 4", f)
	}
}

func TestConnectionLossEndsCallsAndStreams(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	readN(t, a, len("first paint"))
	core.read() // credit

	callErr := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "t7")
		callErr <- err
	}()
	core.readRequest()
	core.conn.Close()

	if err := <-callErr; !errors.Is(err, ErrClosed) {
		t.Fatalf("call err = %v, want ErrClosed", err)
	}
	if _, err := a.Read(make([]byte, 8)); !errors.Is(err, ErrClosed) {
		t.Fatalf("Read err = %v, want ErrClosed", err)
	}
	if _, ok := a.End(); ok {
		t.Fatal("End reported a lost connection as the core ending the stream")
	}
	<-c.Done()
	if _, err := c.Get(context.Background(), "t7"); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after loss err = %v, want ErrClosed", err)
	}
}

func TestOversizedFrameEndsConnection(t *testing.T) {
	c, core := newFake(t)
	h := make([]byte, frameHeader)
	binary.LittleEndian.PutUint32(h[5:9], maxFrame+1)
	core.conn.Write(h)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection still open")
	}
	if err := c.Err(); !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "frame") {
		t.Fatalf("Err = %v", err)
	}
}

func TestOversizedRequestIsRefused(t *testing.T) {
	c, core := newFake(t)
	err := c.Call(context.Background(), "terminal.get", map[string]string{"terminal": strings.Repeat("x", maxFrame)}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want frame size error", err)
	}
	go c.Get(context.Background(), "t8")
	if req := core.readRequest(); req.Method != "terminal.get" || string(req.Params) != `{"terminal":"t8"}` {
		t.Fatalf("next request = %s %s", req.Method, req.Params)
	}
}

func TestSubscribe(t *testing.T) {
	c, core := newFake(t)
	done := make(chan *Subscription, 1)
	go func() {
		s, err := c.Subscribe(context.Background(), map[string]string{"agent-pane.pane": "p1"})
		if err != nil {
			t.Error(err)
		}
		done <- s
	}()
	req := core.readRequest()
	if req.Method != "events.subscribe" || string(req.Params) != `{"labels":{"agent-pane.pane":"p1"}}` {
		t.Fatalf("subscribe request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, map[string]any{"channel": 5, "seq": 8, "terminals": []any{terminalFacts("t7")}})
	s := <-done
	if s == nil {
		t.FailNow()
	}
	defer s.Close()
	if s.Seq != 8 || len(s.Terminals) != 1 {
		t.Fatalf("snapshot = %d %+v", s.Seq, s.Terminals)
	}

	// One event split over two frames, an old event to drop, then two in
	// one frame.
	core.write(5, frameData, []byte(`{"jsonrpc":"2.0","method":"event","params":{"data":{"state":"working"},"seq":9,`))
	core.write(5, frameData, []byte(`"terminal":"t7","type":"status"}}`+"\n"))
	core.write(5, frameData, []byte(`{"jsonrpc":"2.0","method":"event","params":{"seq":7,"terminal":"t7","type":"title"}}`+"\n"+
		`{"jsonrpc":"2.0","method":"event","params":{"seq":10,"terminal":"t7","type":"finished"}}`+"\n"+
		`{"jsonrpc":"2.0","method":"event","params":{"seq":11,"type":"missed","terminals":[]}}`+"\n"))
	ctx := context.Background()
	var got []string
	for range 3 {
		e, err := s.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, e.Type)
		if e.Type == "status" && (e.Terminal != "t7" || string(e.Data) != `{"state":"working"}`) {
			t.Fatalf("status event = %+v", e)
		}
	}
	if strings.Join(got, ",") != "status,finished,missed" || s.Seq != 11 {
		t.Fatalf("events = %v, seq %d", got, s.Seq)
	}

	core.write(5, frameClose, nil)
	if _, err := s.Next(ctx); err != io.EOF {
		t.Fatalf("Next err = %v, want io.EOF", err)
	}
}

func TestHandshakeCancelAfterSuccessLeavesClientUsable(t *testing.T) {
	clientConn, coreConn := net.Pipe()
	core := &fakeCore{t: t, conn: coreConn, r: bufio.NewReader(coreConn), frames: make(chan frame, 64)}
	go func() {
		if _, err := core.r.ReadBytes('\n'); err != nil {
			return
		}
		coreConn.Write([]byte(helloReply(Generation) + "\n"))
		core.readFrames()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := NewClient(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		coreConn.Close()
		c.Close()
	})
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "t7")
		done <- err
	}()
	req := core.readRequest()
	core.reply(req.ID, terminalFacts("t7"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCanceledCallSendsNothing(t *testing.T) {
	c, core := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Kill(ctx, "t7"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	go c.Get(context.Background(), "t8")
	if req := core.readRequest(); req.Method != "terminal.get" {
		t.Fatalf("first request = %s, want terminal.get", req.Method)
	}
}

func TestCallDeadlineCoversBlockedSend(t *testing.T) {
	c, core := newUnbufferedFake(t)
	stallWrites(c)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Kill(ctx, "t7"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	core.readRequest()
	core.readRequest()
	// The withdrawn kill never goes out.
	go c.Get(context.Background(), "t8")
	if req := core.readRequest(); req.Method != "terminal.get" {
		t.Fatalf("request = %s, want the withdrawn kill skipped", req.Method)
	}
}

func TestCanceledCallIsForgotten(t *testing.T) {
	c, core := newFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, "t7")
		done <- err
	}()
	req := core.readRequest()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	if n := pendingCalls(c); n != 0 {
		t.Fatalf("%d calls still pending", n)
	}
	// A late reply is dropped.
	core.reply(req.ID, terminalFacts("t7"))
	go c.Get(context.Background(), "t8")
	core.readRequest()
}

func TestStalledWritesDoNotBlockReadsOrDispatch(t *testing.T) {
	c, core := newUnbufferedFake(t)

	sub := make(chan *Subscription, 1)
	go func() {
		s, _ := c.Subscribe(context.Background(), nil)
		sub <- s
	}()
	req := core.readRequest()
	core.reply(req.ID, map[string]any{"channel": 5, "seq": 0, "terminals": []any{}})
	s := <-sub
	if s == nil {
		t.Fatal("no subscription")
	}

	// An attach whose caller gives up once its request is out.
	ctx, cancel := context.WithCancel(context.Background())
	attachErr := make(chan error, 1)
	go func() {
		_, err := c.Attach(ctx, AttachParams{Terminal: "t7", Cols: 80, Rows: 24})
		attachErr <- err
	}()
	attachReq := core.readRequest()
	cancel()
	if err := <-attachErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}

	stallWrites(c)

	// The late reply opens channel 3, whose CLOSE cannot be written yet. The
	// read loop still delivers the event that follows it.
	core.reply(attachReq.ID, map[string]any{"channel": 3, "terminal": terminalFacts("t7")})
	core.write(5, frameData, []byte(`{"jsonrpc":"2.0","method":"event","params":{"seq":1,"terminal":"t7","type":"created"}}`+"\n"))

	// Reading grants credit that cannot be written yet; Next still returns.
	nctx, ncancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ncancel()
	if e, err := s.Next(nctx); err != nil || e.Type != "created" {
		t.Fatalf("Next = %+v, %v", e, err)
	}

	// Close still unblocks a pending Next.
	nextErr := make(chan error, 1)
	go func() {
		_, err := s.Next(nctx)
		nextErr <- err
	}()
	time.Sleep(10 * time.Millisecond)
	s.Close()
	if err := <-nextErr; !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Next err = %v, want ErrStreamClosed", err)
	}

	core.readRequest()
	core.readRequest()
	var got []string
	for range 3 {
		f := core.read()
		got = append(got, fmt.Sprintf("%d/%d", f.ch, f.kind))
	}
	// The orphan CLOSE, the credit, then the subscription's CLOSE.
	if strings.Join(got, " ") != "3/2 5/1 5/2" {
		t.Fatalf("frames = %v", got)
	}
}

func TestAbandonedStreamCallsAreBounded(t *testing.T) {
	c, core := newFake(t)
	for i := 0; i < maxAbandoned; i++ {
		abandonAttach(t, c, core)
	}
	if n := pendingCalls(c); n != maxAbandoned {
		t.Fatalf("%d calls pending, want %d", n, maxAbandoned)
	}
	select {
	case <-c.Done():
		t.Fatal("connection ended at the bound")
	default:
	}
	abandonAttach(t, c, core)
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection still open past the bound")
	}
	if n := pendingCalls(c); n != 0 {
		t.Fatalf("%d calls still pending", n)
	}
}

// abandonAttach starts an attach the core never answers and cancels it once
// its request is out.
func abandonAttach(t *testing.T, c *Client, core *fakeCore) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Attach(ctx, AttachParams{Terminal: "t7", Cols: 80, Rows: 24})
		done <- err
	}()
	core.readRequest()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
}

func TestAttachReadFrameReturnsAWholeFrame(t *testing.T) {
	c, core := newFake(t)
	a := attach(t, c, core, 3)
	if got := readN(t, a, 5); got != "first" {
		t.Fatalf("Read = %q", got)
	}
	core.read() // credit
	frame, err := a.ReadFrame()
	if err != nil || string(frame) != " paint" {
		t.Fatalf("ReadFrame = %q, %v, want the rest of the first frame", frame, err)
	}
	core.read() // credit
	core.write(3, frameData, []byte("next"))
	if frame, err := a.ReadFrame(); err != nil || string(frame) != "next" {
		t.Fatalf("ReadFrame = %q, %v", frame, err)
	}
}

func TestAttachCloseReleasesABlockedWrite(t *testing.T) {
	c, core := newUnbufferedFake(t)
	a := attach(t, c, core, 3)
	stallWrites(c)
	done := make(chan error, 1)
	go func() {
		_, err := a.Write([]byte("x"))
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	a.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStreamClosed) {
			t.Fatalf("Write err = %v, want ErrStreamClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left a blocked Write waiting")
	}
}

func TestStreamSendIsBounded(t *testing.T) {
	old := writeTimeout
	writeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeTimeout = old })
	c, core := newUnbufferedFake(t)
	a := attach(t, c, core, 3)
	stallWrites(c)
	done := make(chan error, 1)
	go func() { done <- a.Resize(100, 30) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Resize to a core that reads nothing succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Resize to a core that reads nothing never returned")
	}
}

func TestTeamworkCalls(t *testing.T) {
	c, core := newFake(t)
	ctx := context.Background()
	errs := make(chan error, 1)

	var sent SendResult
	go func() {
		var err error
		sent, err = c.Send(ctx, SendParams{Terminal: "t3", Text: "go", Caller: "t1"})
		errs <- err
	}()
	req := core.readRequest()
	if req.Method != "terminal.send" || string(req.Params) != `{"terminal":"t3","text":"go","caller":"t1"}` {
		t.Fatalf("send request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, map[string]any{"turn": 4, "status": "idle", "started": true})
	if err := <-errs; err != nil || sent.Turn != 4 || *sent.Status != "idle" {
		t.Fatalf("Send = %+v, %v", sent, err)
	}

	var waited WaitResult
	go func() {
		var err error
		waited, err = c.Wait(ctx, WaitParams{Terminal: "t3", Caller: "t1", Timeout: 90})
		errs <- err
	}()
	req = core.readRequest()
	if req.Method != "terminal.wait" || string(req.Params) != `{"terminal":"t3","caller":"t1","timeout":90}` {
		t.Fatalf("wait request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, map[string]any{"terminal": "t3", "ended": true, "reason": "status", "status": "done", "turn": 5, "children": []any{}})
	if err := <-errs; err != nil || !waited.Ended || waited.Reason != "status" || *waited.Status != "done" {
		t.Fatalf("Wait = %+v, %v", waited, err)
	}

	var read ReadResult
	go func() {
		var err error
		read, err = c.Read(ctx, ReadParams{Terminal: "t3", Lines: 60})
		errs <- err
	}()
	req = core.readRequest()
	if req.Method != "terminal.read" || string(req.Params) != `{"terminal":"t3","lines":60}` {
		t.Fatalf("read request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, map[string]any{"text": "hello", "complete": true, "how": "history"})
	if err := <-errs; err != nil || read.Text != "hello" {
		t.Fatalf("Read = %+v, %v", read, err)
	}

	go func() {
		_, err := c.Run(ctx, RunParams{Argv: []string{"sh"}, Parent: "t1", NoNotify: true})
		errs <- err
	}()
	req = core.readRequest()
	if req.Method != "terminal.run" || string(req.Params) != `{"argv":["sh"],"parent":"t1","no_notify":true}` {
		t.Fatalf("run request = %s %s", req.Method, req.Params)
	}
	core.reply(req.ID, terminalFacts("t4"))
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}
