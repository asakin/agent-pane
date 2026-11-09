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
	"sync"
	"time"
)

// Frame kinds on a framed connection. Every frame is a little-endian u32
// channel, a u8 kind and a u32 payload length, then the payload. Channel 0
// carries JSON-RPC messages as DATA; terminal.attach and events.subscribe
// open the others.
const (
	frameData   byte = 0
	frameCredit byte = 1 // u32: bytes of the channel's DATA the reader consumed
	frameClose  byte = 2 // from the core: JSON facts on why the stream ended
	frameResize byte = 3 // u16 cols, u16 rows
)

const frameHeader = 9

// maxFrame bounds a frame from the core, so a corrupt length cannot make the
// client allocate gigabytes.
const maxFrame = 16 << 20

// maxDataFrame splits writes to a stream into frames no larger than this.
const maxDataFrame = 64 << 10

// ErrUnsupportedGeneration reports a core that does not speak Generation.
var ErrUnsupportedGeneration = errors.New("unsupported herdr-core protocol generation")

// ErrClosed reports a call or stream on a connection that has ended.
var ErrClosed = errors.New("herdr-core connection closed")

// Error is an error reply from the core. Kind is the core's stable
// data.kind, such as "not_found" or "finished".
type Error struct {
	Code    int
	Message string
	Kind    string
	Hint    string
}

func (e *Error) Error() string {
	s := "herdr-core: " + e.Message
	if e.Kind != "" {
		s += " (" + e.Kind + ")"
	}
	if e.Hint != "" {
		s += ": " + e.Hint
	}
	return s
}

// IsKind reports whether err is an error reply from the core of kind.
func IsKind(err error, kind string) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == kind
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Kind string `json:"kind"`
		Hint string `json:"hint"`
	} `json:"data"`
}

func (e *rpcError) toError() *Error {
	return &Error{Code: e.Code, Message: e.Message, Kind: e.Data.Kind, Hint: e.Data.Hint}
}

type rpcResponse struct {
	ID     *uint64         `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type pendingCall struct {
	done chan rpcResponse
	// opens marks a call whose reply opens a channel (terminal.attach,
	// events.subscribe). The read loop registers it as stream before it
	// reads another frame, so the stream sees all of its DATA.
	opens  bool
	stream *channel
	// abandoned marks a call whose caller gave up; a channel its reply
	// opens is closed straight away.
	abandoned bool
}

// outFrame is a frame waiting for the writer. done, when set, receives the
// write's result.
type outFrame struct {
	buf  []byte
	done chan error
}

// Client is one framed connection to a herdr-core. It is safe for concurrent
// use.
type Client struct {
	conn  net.Conn
	Hello HelloResult

	// The writer goroutine sends out in order, so no caller waits on the
	// socket while holding anything another caller or the read loop needs.
	wmu        sync.Mutex
	out        []*outFrame
	werr       error // set once the writer stops
	wake       chan struct{}
	writerDone chan struct{}

	mu      sync.Mutex
	nextID  uint64
	calls   map[uint64]*pendingCall
	streams map[uint32]*channel
	err     error // set once the read loop ends
	done    chan struct{}
}

// NewClient performs the handshake on conn and switches it to framed mode.
// It fails with ErrUnsupportedGeneration when the core does not speak
// Generation. The client owns conn from here on, also on error.
func NewClient(ctx context.Context, conn net.Conn) (*Client, error) {
	hello, r, err := handshake(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c := &Client{
		conn:       conn,
		Hello:      hello,
		wake:       make(chan struct{}, 1),
		writerDone: make(chan struct{}),
		nextID:     1,
		calls:      map[uint64]*pendingCall{},
		streams:    map[uint32]*channel{},
		done:       make(chan struct{}),
	}
	go c.readLoop(r)
	go c.writeLoop()
	return c, nil
}

func handshake(ctx context.Context, conn net.Conn) (HelloResult, *bufio.Reader, error) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		conn.SetDeadline(time.Unix(1, 0))
		close(fired)
	})
	hello, r, err := exchangeHello(ctx, conn)
	if !stop() {
		// ctx ended during the exchange: wait for the callback, so the
		// deadline it sets is cleared below rather than after.
		<-fired
	}
	conn.SetDeadline(time.Time{})
	return hello, r, err
}

func exchangeHello(ctx context.Context, conn net.Conn) (HelloResult, *bufio.Reader, error) {
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "hello",
		"params":  map[string]any{"generation": Generation, "framed": true},
	})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return HelloResult{}, nil, handshakeErr(ctx, err)
	}
	// The reply is the last line-mode message; frames follow it on r.
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return HelloResult{}, nil, handshakeErr(ctx, err)
	}
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return HelloResult{}, nil, fmt.Errorf("herdr-core hello: %w", err)
	}
	if resp.Error != nil {
		e := resp.Error.toError()
		if e.Kind == "unsupported_generation" {
			return HelloResult{}, nil, fmt.Errorf("%w: Agent Pane speaks generation %d, %w", ErrUnsupportedGeneration, Generation, e)
		}
		return HelloResult{}, nil, e
	}
	var hello HelloResult
	if err := json.Unmarshal(resp.Result, &hello); err != nil {
		return HelloResult{}, nil, fmt.Errorf("herdr-core hello: %w", err)
	}
	if hello.Generation != Generation {
		return HelloResult{}, nil, fmt.Errorf("%w: herdr-core %s speaks generation %d, Agent Pane speaks %d",
			ErrUnsupportedGeneration, hello.Version, hello.Generation, Generation)
	}
	if !hello.Framed {
		return HelloResult{}, nil, fmt.Errorf("herdr-core %s did not switch to framed mode", hello.Version)
	}
	return hello, r, nil
}

func handshakeErr(ctx context.Context, err error) error {
	// The connection's deadline is ctx's, and can pass a moment before ctx
	// reports it.
	if ctx.Err() != nil {
		return fmt.Errorf("herdr-core hello: %w", ctx.Err())
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return fmt.Errorf("herdr-core hello: %w", context.DeadlineExceeded)
	}
	return fmt.Errorf("herdr-core hello: %w", err)
}

// Close ends the connection, every call waiting on it and its streams.
// Terminals keep running in the core.
func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	<-c.writerDone
	return err
}

// Done is closed once the connection has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended, nil while it is open.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) readLoop(r io.Reader) {
	var err error
	header := make([]byte, frameHeader)
	for {
		if _, err = io.ReadFull(r, header); err != nil {
			break
		}
		ch := binary.LittleEndian.Uint32(header[0:4])
		kind := header[4]
		n := binary.LittleEndian.Uint32(header[5:9])
		if n > maxFrame {
			err = fmt.Errorf("herdr-core sent a %d-byte frame", n)
			break
		}
		payload := make([]byte, n)
		if _, err = io.ReadFull(r, payload); err != nil {
			break
		}
		if ch == 0 {
			if kind == frameData {
				c.handleResponse(payload)
			}
			continue
		}
		c.mu.Lock()
		s := c.streams[ch]
		if kind == frameClose {
			delete(c.streams, ch)
		}
		c.mu.Unlock()
		if s == nil {
			continue
		}
		switch kind {
		case frameData:
			s.push(payload)
		case frameClose:
			s.end(payload, io.EOF)
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = ErrClosed
	} else {
		err = fmt.Errorf("%w: %w", ErrClosed, err)
	}
	c.conn.Close()

	c.mu.Lock()
	c.err = err
	calls, streams := c.calls, c.streams
	c.calls, c.streams = map[uint64]*pendingCall{}, map[uint32]*channel{}
	c.mu.Unlock()
	for _, call := range calls {
		close(call.done)
	}
	for _, s := range streams {
		s.end(nil, err)
	}
	close(c.done)
}

func (c *Client) handleResponse(payload []byte) {
	var resp rpcResponse
	if json.Unmarshal(payload, &resp) != nil || resp.ID == nil {
		// Not a reply: the core sends no notifications on channel 0 to a
		// client that has not asked for them.
		return
	}
	var orphan uint32
	c.mu.Lock()
	call := c.calls[*resp.ID]
	delete(c.calls, *resp.ID)
	if call != nil && call.opens && resp.Error == nil {
		var r struct {
			Channel uint32 `json:"channel"`
		}
		if json.Unmarshal(resp.Result, &r) == nil && r.Channel != 0 {
			if call.abandoned {
				orphan = r.Channel
			} else {
				call.stream = newChannel(c, r.Channel)
				c.streams[r.Channel] = call.stream
			}
		}
	}
	c.mu.Unlock()
	if orphan != 0 {
		c.postFrame(orphan, frameClose, nil)
	}
	if call != nil && !call.abandoned {
		call.done <- resp
	}
}

func (c *Client) writeLoop() {
	defer close(c.writerDone)
	for {
		c.wmu.Lock()
		if len(c.out) == 0 {
			c.wmu.Unlock()
			select {
			case <-c.wake:
				continue
			case <-c.done:
				c.stopWrites(c.Err())
				return
			}
		}
		// Taken off the queue, the frame is under way and can no longer be
		// withdrawn.
		f := c.out[0]
		c.out = c.out[1:]
		c.wmu.Unlock()
		_, err := c.conn.Write(f.buf)
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrClosed, err)
		}
		if f.done != nil {
			f.done <- err
		}
		if err != nil {
			// A partial frame leaves the connection unusable.
			c.conn.Close()
			c.stopWrites(err)
			return
		}
	}
}

func (c *Client) stopWrites(err error) {
	c.wmu.Lock()
	c.werr = err
	out := c.out
	c.out = nil
	c.wmu.Unlock()
	for _, f := range out {
		if f.done != nil {
			f.done <- err
		}
	}
}

func (c *Client) queueFrame(ch uint32, kind byte, payload []byte, wait bool) (*outFrame, error) {
	f := &outFrame{buf: make([]byte, frameHeader+len(payload))}
	binary.LittleEndian.PutUint32(f.buf[0:4], ch)
	f.buf[4] = kind
	binary.LittleEndian.PutUint32(f.buf[5:9], uint32(len(payload)))
	copy(f.buf[frameHeader:], payload)
	if wait {
		f.done = make(chan error, 1)
	}
	c.wmu.Lock()
	if c.werr != nil {
		err := c.werr
		c.wmu.Unlock()
		return nil, err
	}
	c.out = append(c.out, f)
	c.wmu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return f, nil
}

// postFrame queues a frame without waiting for it to be written. A frame
// that cannot be written shows up as the connection ending.
func (c *Client) postFrame(ch uint32, kind byte, payload []byte) {
	_, _ = c.queueFrame(ch, kind, payload, false)
}

// writeFrame sends a frame and waits until it is written or ctx ends. When
// ctx ends first, sent reports whether the frame was already under way; a
// frame still queued is withdrawn and never sent.
func (c *Client) writeFrame(ctx context.Context, ch uint32, kind byte, payload []byte) (sent bool, err error) {
	f, err := c.queueFrame(ch, kind, payload, true)
	if err != nil {
		return false, err
	}
	select {
	case err := <-f.done:
		return err == nil, err
	case <-ctx.Done():
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	for i, q := range c.out {
		if q == f {
			c.out = append(c.out[:i], c.out[i+1:]...)
			return false, ctx.Err()
		}
	}
	return true, ctx.Err()
}

// Call sends method with params and decodes the reply's result into result,
// which may be nil. An error reply is an *Error.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	_, err := c.call(ctx, method, params, result, false)
	return err
}

func (c *Client) call(ctx context.Context, method string, params, result any, opens bool) (*channel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	id := c.nextID
	c.nextID++
	call := &pendingCall{done: make(chan rpcResponse, 1), opens: opens}
	c.calls[id] = call
	c.mu.Unlock()

	if params == nil {
		params = struct{}{}
	}
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	sent := false
	if err == nil {
		sent, err = c.writeFrame(ctx, 0, frameData, req)
	}
	if err != nil {
		if sent {
			// ctx ended while the request was going out.
			c.abandon(id, call)
			return nil, err
		}
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case resp, ok := <-call.done:
		if !ok {
			return nil, c.Err()
		}
		if resp.Error != nil {
			return nil, resp.Error.toError()
		}
		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				if call.stream != nil {
					call.stream.Close()
				}
				return nil, fmt.Errorf("herdr-core %s: %w", method, err)
			}
		}
		return call.stream, nil
	case <-ctx.Done():
		c.abandon(id, call)
		return nil, ctx.Err()
	}
}

// abandon gives up on a call whose request was sent.
func (c *Client) abandon(id uint64, call *pendingCall) {
	c.mu.Lock()
	pending := c.calls[id] == call
	if pending {
		if call.opens {
			// Leave it registered, so a channel its reply opens is closed.
			call.abandoned = true
		} else {
			delete(c.calls, id)
		}
	}
	c.mu.Unlock()
	if !pending {
		// The reply came as ctx ended.
		if resp, ok := <-call.done; ok && resp.Error == nil && call.stream != nil {
			call.stream.Close()
		}
	}
}

// channel is one stream on the connection: received DATA is queued until read,
// and reading grants the core credit for it, so the queue stays within the
// core's credit window.
type channel struct {
	c  *Client
	id uint32

	mu     sync.Mutex
	ready  chan struct{} // signalled when data arrives or the stream ends
	queue  [][]byte
	facts  json.RawMessage
	err    error // io.EOF once the core closed the stream
	closed bool  // closed by this side
}

func newChannel(c *Client, id uint32) *channel {
	return &channel{c: c, id: id, ready: make(chan struct{}, 1)}
}

func (s *channel) signal() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *channel) push(p []byte) {
	s.mu.Lock()
	if s.err == nil && !s.closed && len(p) > 0 {
		s.queue = append(s.queue, p)
	}
	s.mu.Unlock()
	s.signal()
}

func (s *channel) end(facts json.RawMessage, err error) {
	s.mu.Lock()
	if s.err == nil {
		s.facts = facts
		s.err = err
	}
	s.mu.Unlock()
	s.signal()
}

// read copies queued data into p, blocking until there is some or the stream
// ends.
func (s *channel) read(ctx context.Context, p []byte) (int, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, ErrStreamClosed
		}
		if len(s.queue) > 0 {
			n := copy(p, s.queue[0])
			if n == len(s.queue[0]) {
				s.queue = s.queue[1:]
			} else {
				s.queue[0] = s.queue[0][n:]
			}
			ended := s.err != nil
			s.mu.Unlock()
			if !ended && n > 0 {
				var credit [4]byte
				binary.LittleEndian.PutUint32(credit[:], uint32(n))
				s.c.postFrame(s.id, frameCredit, credit[:])
			}
			return n, nil
		}
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return 0, err
		}
		s.mu.Unlock()
		select {
		case <-s.ready:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func (s *channel) send(kind byte, p []byte) error {
	s.mu.Lock()
	closed, err := s.closed, s.err
	s.mu.Unlock()
	if closed {
		return ErrStreamClosed
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return ErrStreamEnded
		}
		return err
	}
	_, err = s.c.writeFrame(context.Background(), s.id, kind, p)
	return err
}

// Close tells the core this side is done with the stream. It is idempotent.
func (s *channel) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ended := s.err != nil
	s.queue = nil
	s.mu.Unlock()
	s.signal()

	s.c.mu.Lock()
	if s.c.streams[s.id] == s {
		delete(s.c.streams, s.id)
	}
	s.c.mu.Unlock()
	if !ended {
		s.c.postFrame(s.id, frameClose, nil)
	}
	return nil
}

// ErrStreamClosed reports a read or write on a stream this side closed.
var ErrStreamClosed = errors.New("herdr-core stream closed")

// ErrStreamEnded reports a write to a stream the core ended.
var ErrStreamEnded = errors.New("herdr-core stream ended")
