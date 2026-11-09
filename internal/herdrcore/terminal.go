package herdrcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// HelloResult is the core's handshake reply.
type HelloResult struct {
	Generation int      `json:"generation"`
	Version    string   `json:"version"`
	Core       CoreInfo `json:"core"`
	Framed     bool     `json:"framed"`
	Methods    []string `json:"methods"`
	Features   []string `json:"features"`
}

// CoreInfo identifies a core.
type CoreInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Terminal is one terminal's facts, the fields of the core's Terminal the
// host reads. State is "live" or "finished".
type Terminal struct {
	Address    string            `json:"address"`
	Name       *string           `json:"name"`
	Labels     map[string]string `json:"labels"`
	Argv       []string          `json:"argv"`
	Cwd        string            `json:"cwd"`
	State      string            `json:"state"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Pid        *int              `json:"pid"`
	ExitCode   *int              `json:"exit_code"`
	Signal     *int              `json:"signal"`
	Reason     *string           `json:"reason"`
	Keep       bool              `json:"keep"`
	Title      string            `json:"title"`
	Status     *Status           `json:"status"`
	CreatedMs  uint64            `json:"created_ms"`
	FinishedMs *uint64           `json:"finished_ms"`
}

// Status is a program's own report of what it is doing: idle, working,
// done, blocked or error.
type Status struct {
	State string `json:"state"`
	Msg   string `json:"msg,omitempty"`
	Title string `json:"title,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

// Size is a terminal size in cells.
type Size struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// RunParams starts a program in a new terminal. With an empty Argv the core
// starts the machine's shell.
type RunParams struct {
	Argv   []string          `json:"argv,omitempty"`
	Cwd    string            `json:"cwd,omitempty"`
	Env    map[string]string `json:"env,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	Name   string            `json:"name,omitempty"`
	Size   *Size             `json:"size,omitempty"`
	Keep   bool              `json:"keep,omitempty"`
	// NoParent keeps the core from making it a child of the terminal the
	// caller runs in, when it runs in one.
	NoParent bool `json:"no_parent,omitempty"`
	// Parent makes it a child of that terminal: it ends when the parent's
	// program ends.
	Parent string `json:"parent,omitempty"`
	// NoNotify keeps the core from typing notes about it into its parent.
	NoNotify bool `json:"no_notify,omitempty"`
}

// Run starts a terminal and returns its facts once its program runs.
func (c *Client) Run(ctx context.Context, p RunParams) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.run", p, &t)
	return t, err
}

// List returns the live terminals carrying every label in labels, and the
// finished ones too when all is set.
func (c *Client) List(ctx context.Context, labels map[string]string, all bool) ([]Terminal, error) {
	var r struct {
		Terminals []Terminal `json:"terminals"`
	}
	err := c.Call(ctx, "terminal.list", map[string]any{"labels": nonNil(labels), "all": all}, &r)
	return r.Terminals, err
}

// Get returns one terminal's facts, live or finished.
func (c *Client) Get(ctx context.Context, terminal string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.get", map[string]any{"terminal": terminal}, &t)
	return t, err
}

// Kill ends a terminal's program and returns its facts after.
func (c *Client) Kill(ctx context.Context, terminal string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.kill", map[string]any{"terminal": terminal}, &t)
	return t, err
}

// Remove removes a finished terminal. A live one fails with kind "live".
func (c *Client) Remove(ctx context.Context, terminal string) error {
	return c.Call(ctx, "terminal.rm", map[string]any{"terminal": terminal}, nil)
}

// SendParams pastes Text into a terminal and presses Enter. Caller is the
// sender's terminal, so a Wait from it waits for the turn this send started.
// The core refuses a blocked program unless Force.
type SendParams struct {
	Terminal string `json:"terminal"`
	Text     string `json:"text"`
	Caller   string `json:"caller,omitempty"`
	Force    bool   `json:"force,omitempty"`
}

// SendResult is the program's status word and turn when the text went in.
type SendResult struct {
	Turn   uint64  `json:"turn"`
	Status *string `json:"status"`
}

// Send types text into a terminal.
func (c *Client) Send(ctx context.Context, p SendParams) (SendResult, error) {
	var r SendResult
	err := c.Call(ctx, "terminal.send", p, &r)
	return r, err
}

// WaitParams waits until a terminal's program is not working. Timeout, in
// seconds, is when the core checks in instead (0: the core's default).
type WaitParams struct {
	Terminal string  `json:"terminal"`
	Caller   string  `json:"caller,omitempty"`
	Timeout  float64 `json:"timeout,omitempty"`
}

// WaitResult is how a wait ended. Ended is false on a timeout, when the
// program is still working. Reason is status, exit, timeout, not_started or
// message (someone messaged the waiter).
type WaitResult struct {
	Terminal string  `json:"terminal"`
	Ended    bool    `json:"ended"`
	Reason   string  `json:"reason"`
	Status   *string `json:"status"`
	ExitCode *int    `json:"exit_code"`
	Signal   *int    `json:"signal"`
}

// Wait waits until the terminal's program stops working, exits, or the
// timeout passes.
func (c *Client) Wait(ctx context.Context, p WaitParams) (WaitResult, error) {
	var r WaitResult
	err := c.Call(ctx, "terminal.wait", p, &r)
	return r, err
}

// ReadParams reads a terminal's screen plus Lines of history.
type ReadParams struct {
	Terminal string `json:"terminal"`
	Lines    int    `json:"lines,omitempty"`
	Caller   string `json:"caller,omitempty"`
}

// ReadResult is a terminal's text.
type ReadResult struct {
	Text     string `json:"text"`
	Complete bool   `json:"complete"`
}

// Read returns a terminal's screen and history as text, live or finished.
func (c *Client) Read(ctx context.Context, p ReadParams) (ReadResult, error) {
	var r ReadResult
	err := c.Call(ctx, "terminal.read", p, &r)
	return r, err
}

// AttachParams opens a stream to a live terminal at the viewer's size.
// History is how many scrollback lines the first paint carries.
type AttachParams struct {
	Terminal string `json:"terminal"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	History  int    `json:"history,omitempty"`
}

// Attach opens a raw stream to a live terminal: the first paint, then live
// output. Writes are keys; Resize sets the viewer's size.
func (c *Client) Attach(ctx context.Context, p AttachParams) (*Attachment, error) {
	if err := checkSize(p.Cols, p.Rows); err != nil {
		return nil, err
	}
	var r struct {
		Terminal Terminal `json:"terminal"`
	}
	ch, err := c.call(ctx, "terminal.attach", p, &r, true)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, errors.New("herdr-core terminal.attach returned no channel")
	}
	return &Attachment{Terminal: r.Terminal, ch: ch}, nil
}

// Attachment is a stream attached to one terminal.
type Attachment struct {
	// Terminal is the terminal's facts at attach.
	Terminal Terminal
	ch       *channel
}

// Read reads the terminal's output. It returns io.EOF once the core ended
// the stream (see End), and ErrStreamClosed after Close.
func (a *Attachment) Read(p []byte) (int, error) {
	return a.ch.read(context.Background(), p)
}

// ReadFrame returns the next DATA frame of output whole, such as the first
// paint, which the core sends as one frame straight after the attach reply.
// What a Read left of a frame comes back as that frame.
func (a *Attachment) ReadFrame() ([]byte, error) {
	return a.ch.readFrame(context.Background())
}

// Write sends p to the terminal as input.
func (a *Attachment) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), maxDataFrame)
		if err := a.ch.send(frameData, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Resize sets the viewer's size. The core applies the last active viewer's
// size to the terminal.
func (a *Attachment) Resize(cols, rows int) error {
	if err := checkSize(cols, rows); err != nil {
		return err
	}
	var p [4]byte
	binary.LittleEndian.PutUint16(p[0:2], uint16(cols))
	binary.LittleEndian.PutUint16(p[2:4], uint16(rows))
	return a.ch.send(frameResize, p[:])
}

// StreamEnd is why the core ended an attach stream: the terminal's program
// ended (Reason "exited" or "killed", with ExitCode or Signal), or the
// terminal was lost.
type StreamEnd struct {
	Reason     string `json:"reason"`
	ExitCode   *int   `json:"exit_code"`
	Signal     *int   `json:"signal"`
	FinishedMs uint64 `json:"finished_ms"`
}

// End reports why the core ended the stream, once Read has returned io.EOF.
// It is false while the stream is open, and after the connection, rather
// than the core, ended it.
func (a *Attachment) End() (StreamEnd, bool) {
	a.ch.mu.Lock()
	facts, err := a.ch.facts, a.ch.err
	a.ch.mu.Unlock()
	if !errors.Is(err, io.EOF) {
		return StreamEnd{}, false
	}
	var end StreamEnd
	if len(facts) > 0 {
		_ = json.Unmarshal(facts, &end)
	}
	return end, true
}

// Close detaches. The terminal keeps running. A blocked Read, Write or
// Resize returns ErrStreamClosed.
func (a *Attachment) Close() error {
	return a.ch.Close()
}

func checkSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > 0xffff || rows > 0xffff {
		return fmt.Errorf("herdr-core: invalid terminal size %dx%d", cols, rows)
	}
	return nil
}

// Event is one fact change from events.subscribe. Type is the core's event
// type (created, lost, restarted, keep, finished, removed, status, turn,
// title, labels, size, doc), "missed" when the subscriber fell behind (a
// fresh snapshot is in Terminals), or "heartbeat".
type Event struct {
	Seq      uint64          `json:"seq"`
	Type     string          `json:"type"`
	Terminal string          `json:"terminal"`
	Data     json.RawMessage `json:"data"`
	// Terminals is the snapshot a "missed" event carries.
	Terminals []Terminal `json:"terminals"`
}

// Subscribe returns the terminals carrying every label in labels and a
// subscription to their fact changes after that snapshot.
func (c *Client) Subscribe(ctx context.Context, labels map[string]string) (*Subscription, error) {
	var r struct {
		Seq       uint64     `json:"seq"`
		Terminals []Terminal `json:"terminals"`
	}
	ch, err := c.call(ctx, "events.subscribe", map[string]any{"labels": nonNil(labels)}, &r, true)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, errors.New("herdr-core events.subscribe returned no channel")
	}
	return &Subscription{Seq: r.Seq, Terminals: r.Terminals, ch: ch}, nil
}

// Subscription is an event stream, one JSON-RPC notification per line.
type Subscription struct {
	// Seq and Terminals are the snapshot: events after Seq follow.
	Seq       uint64
	Terminals []Terminal
	ch        *channel
	buf       []byte
}

// Next returns the next event. Events whose Seq is not above the last
// snapshot's (Seq, or a "missed" event's) are dropped, as the core asks.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		if i := bytes.IndexByte(s.buf, '\n'); i >= 0 {
			line := s.buf[:i]
			s.buf = s.buf[i+1:]
			var n struct {
				Method string `json:"method"`
				Params Event  `json:"params"`
			}
			if err := json.Unmarshal(line, &n); err != nil {
				return Event{}, fmt.Errorf("herdr-core event: %w", err)
			}
			e := n.Params
			if e.Type == "" {
				e.Type = n.Method
			}
			switch {
			case e.Type == "missed":
				if e.Seq > s.Seq {
					s.Seq = e.Seq
				}
				return e, nil
			case e.Type == "heartbeat" || n.Method == "heartbeat":
				e.Type = "heartbeat"
				return e, nil
			case e.Seq <= s.Seq:
				continue
			}
			s.Seq = e.Seq
			return e, nil
		}
		p := make([]byte, 32<<10)
		n, err := s.ch.read(ctx, p)
		if err != nil {
			return Event{}, err
		}
		s.buf = append(s.buf, p[:n]...)
	}
}

// Close ends the subscription.
func (s *Subscription) Close() error { return s.ch.Close() }

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// Label sets labels on a terminal; an empty value removes that label.
func (c *Client) Label(ctx context.Context, terminal string, labels map[string]string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.label", map[string]any{"terminal": terminal, "labels": nonNil(labels)}, &t)
	return t, err
}
