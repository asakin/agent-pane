//go:build windows

package herdrcore

// A small named-pipe client on golang.org/x/sys/windows, so the core client
// gets a net.Conn on Windows without a new dependency (RFC 0002, "Client").
//
// The client needs deadlines that interrupt I/O already in flight: the
// handshake sets a deadline in the past when its context ends, and the
// writer sets a write deadline before every frame. An os.File made from a
// pipe handle the runtime did not open has no deadline support, so the
// connection does its own overlapped I/O:
//
//   - The handle is opened with FILE_FLAG_OVERLAPPED. Each Read or Write
//     starts one overlapped operation, signalled through that direction's
//     own event, and waits for it with GetOverlappedResult. One event per
//     direction lets a read and a write be in flight together.
//   - A deadline is a timer per direction. When it passes, it marks the
//     direction expired and cancels the operation in flight with
//     CancelIoEx; the operation then ends with ERROR_OPERATION_ABORTED,
//     reported as os.ErrDeadlineExceeded. Later calls fail at once until
//     the deadline is moved.
//   - Close cancels both directions, waits for their operations to end and
//     only then closes the handle, so no operation outlives it.

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows has no WaitNamedPipe.
var procWaitNamedPipeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

// errPipeNotConnected and errNoData are what a pipe whose other end has
// gone reports, beside ERROR_BROKEN_PIPE.
const (
	errPipeNotConnected = windows.Errno(233) // ERROR_PIPE_NOT_CONNECTED
	errNoData           = windows.Errno(232) // ERROR_NO_DATA, the pipe is being closed
)

// dialPipe opens the named pipe name, a \\.\pipe\... path. While every
// instance of the pipe is busy it waits with WaitNamedPipe and tries again,
// until ctx ends.
func dialPipe(ctx context.Context, name string) (net.Conn, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, pipeErr("dial", name, err)
	}
	for {
		// SECURITY_IDENTIFICATION: the server may learn who we are but not
		// act as us, so a process squatting the name cannot borrow our
		// rights. herdr-core's own client opens its pipe the same way.
		h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			c, err := newPipeConn(h, name)
			if err != nil {
				windows.CloseHandle(h)
				return nil, pipeErr("dial", name, err)
			}
			return c, nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, pipeErr("dial", name, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, pipeErr("dial", name, err)
		}
		wait := 100 * time.Millisecond
		if deadline, ok := ctx.Deadline(); ok {
			wait = min(wait, time.Until(deadline))
		}
		if wait <= 0 {
			return nil, pipeErr("dial", name, context.DeadlineExceeded)
		}
		// Returns when an instance is free or the wait ends; either way
		// the loop tries again. A zero timeout would mean the pipe's
		// default, so wait at least 1ms.
		procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(path)), uintptr(max(wait.Milliseconds(), 1)))
	}
}

// pipeConn is a net.Conn on an overlapped pipe handle. It works on either
// end of a pipe; the tests use it for their fake server too.
type pipeConn struct {
	h      windows.Handle
	name   string
	closed atomic.Bool
	rd, wr pipeDir
}

// pipeDir is one direction of a pipeConn.
type pipeDir struct {
	// op is held for a whole Read or Write: one operation at a time.
	op sync.Mutex
	ev windows.Handle

	// mu guards the fields below. The deadline timer, SetDeadline and
	// Close take it to cancel pending.
	mu      sync.Mutex
	timer   *time.Timer
	expired bool
	pending *windows.Overlapped
	// buf holds the operation's buffer while the kernel may write to it.
	// Keeping it in this heap object also makes the compiler put it on the
	// heap, where it does not move.
	buf []byte
}

func newPipeConn(h windows.Handle, name string) (*pipeConn, error) {
	c := &pipeConn{h: h, name: name}
	var err error
	// Manual-reset events, as GetOverlappedResult expects; ReadFile and
	// WriteFile reset them when they start.
	if c.rd.ev, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
		return nil, err
	}
	if c.wr.ev, err = windows.CreateEvent(nil, 1, 0, nil); err != nil {
		windows.CloseHandle(c.rd.ev)
		return nil, err
	}
	return c, nil
}

func (c *pipeConn) Read(b []byte) (int, error) {
	n, err := c.do(&c.rd, b, true)
	if err != nil {
		if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, errPipeNotConnected) || errors.Is(err, errNoData) {
			return n, io.EOF
		}
		return n, pipeErr("read", c.name, err)
	}
	return n, nil
}

func (c *pipeConn) Write(b []byte) (int, error) {
	// A pipe write can complete short only when it fails, but loop anyway
	// so Write keeps io.Writer's promise.
	written := 0
	for written < len(b) {
		n, err := c.do(&c.wr, b[written:], false)
		written += n
		if err != nil {
			return written, pipeErr("write", c.name, err)
		}
	}
	return written, nil
}

// do runs one overlapped read or write in direction d and waits for it.
func (c *pipeConn) do(d *pipeDir, b []byte, read bool) (int, error) {
	d.op.Lock()
	defer d.op.Unlock()
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	d.mu.Lock()
	expired := d.expired
	d.mu.Unlock()
	if expired {
		return 0, os.ErrDeadlineExceeded
	}
	if len(b) == 0 {
		return 0, nil
	}

	o := &windows.Overlapped{HEvent: d.ev}
	d.buf = b
	defer func() { d.buf = nil }()
	var n uint32
	var err error
	if read {
		err = windows.ReadFile(c.h, b, &n, o)
	} else {
		err = windows.WriteFile(c.h, b, &n, o)
	}
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, os.NewSyscallError(opName(read), err)
	}

	// Publish the operation so a deadline or Close can cancel it. One that
	// passed or closed before this point is caught here instead.
	d.mu.Lock()
	d.pending = o
	cancel := d.expired || c.closed.Load()
	d.mu.Unlock()
	if cancel {
		windows.CancelIoEx(c.h, o)
	}
	// Always wait, also after a cancel: the kernel owns o and b until the
	// operation ends.
	err = windows.GetOverlappedResult(c.h, o, &n, true)
	d.mu.Lock()
	d.pending = nil
	expired = d.expired
	d.mu.Unlock()

	if err == nil {
		// Done, even if a cancel came too late to stop it.
		return int(n), nil
	}
	switch {
	case c.closed.Load():
		return int(n), net.ErrClosed
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED) && expired:
		return int(n), os.ErrDeadlineExceeded
	}
	return int(n), os.NewSyscallError(opName(read), err)
}

func opName(read bool) string {
	if read {
		return "ReadFile"
	}
	return "WriteFile"
}

// Close cancels both directions' operations, waits for them to end, and
// closes the handle.
func (c *pipeConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return pipeErr("close", c.name, net.ErrClosed)
	}
	for _, d := range []*pipeDir{&c.rd, &c.wr} {
		d.mu.Lock()
		if d.pending != nil {
			windows.CancelIoEx(c.h, d.pending)
		}
		if d.timer != nil {
			d.timer.Stop()
			d.timer = nil
		}
		d.mu.Unlock()
	}
	// Operations already cancelled end promptly; ones not yet started see
	// closed and do nothing.
	c.rd.op.Lock()
	c.wr.op.Lock()
	defer c.wr.op.Unlock()
	defer c.rd.op.Unlock()
	err := windows.CloseHandle(c.h)
	windows.CloseHandle(c.rd.ev)
	windows.CloseHandle(c.wr.ev)
	if err != nil {
		return pipeErr("close", c.name, err)
	}
	return nil
}

func (c *pipeConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *pipeConn) SetReadDeadline(t time.Time) error {
	return c.setDeadline(&c.rd, t)
}

func (c *pipeConn) SetWriteDeadline(t time.Time) error {
	return c.setDeadline(&c.wr, t)
}

// setDeadline sets d's deadline to t, cancelling d's operation in flight
// when t has passed and arming a timer to do so when it is ahead. A zero t
// clears the deadline.
func (c *pipeConn) setDeadline(d *pipeDir, t time.Time) error {
	if c.closed.Load() {
		return pipeErr("set deadline", c.name, net.ErrClosed)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	d.expired = false
	if t.IsZero() {
		return nil
	}
	wait := time.Until(t)
	if wait <= 0 {
		c.expire(d)
		return nil
	}
	var timer *time.Timer
	timer = time.AfterFunc(wait, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		// A timer stopped too late to keep it from running must not
		// expire a deadline set after it.
		if d.timer == timer {
			c.expire(d)
		}
	})
	d.timer = timer
	return nil
}

// expire marks d's deadline passed and cancels its operation in flight.
// d.mu must be held. pending is set only while the handle is open, so this
// never cancels on a closed handle.
func (c *pipeConn) expire(d *pipeDir) {
	d.expired = true
	if d.pending != nil {
		windows.CancelIoEx(c.h, d.pending)
	}
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.name) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.name) }

// pipeAddr is a pipe's name as a net.Addr.
type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

// pipeErr wraps err as net does, keeping errors.Is working for
// net.ErrClosed, os.ErrDeadlineExceeded and the windows.Errno values.
func pipeErr(op, name string, err error) error {
	return &net.OpError{Op: op, Net: "pipe", Addr: pipeAddr(name), Err: err}
}
