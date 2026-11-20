//go:build windows

package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var pipeSeq atomic.Int64

// testPipeName is a pipe name no other test or run uses.
func testPipeName(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\agent-pane-test-%d-%d-%d.sock`, os.Getpid(), time.Now().UnixNano(), pipeSeq.Add(1))
}

// pipeServer is a fake core's end: instances of a named pipe, served with
// the same overlapped pipeConn the client uses.
type pipeServer struct {
	t         *testing.T
	name      string
	instances uint32
	first     bool
}

func newPipeServer(t *testing.T, instances uint32) *pipeServer {
	return &pipeServer{t: t, name: testPipeName(t), instances: instances, first: true}
}

// listen creates one instance of the pipe; accept waits for a client on it.
func (s *pipeServer) listen() windows.Handle {
	s.t.Helper()
	name, err := windows.UTF16PtrFromString(s.name)
	if err != nil {
		s.t.Fatal(err)
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if s.first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
		s.first = false
	}
	h, err := windows.CreateNamedPipe(name, flags,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		s.instances, 4096, 4096, 0, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return h
}

func (s *pipeServer) accept(h windows.Handle) (*pipeConn, error) {
	if err := connectInstance(h); err != nil {
		return nil, err
	}
	return newPipeConn(h, s.name)
}

// connectInstance waits for a client to open the pipe instance h.
func connectInstance(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ev)
	o := &windows.Overlapped{HEvent: ev}
	err = windows.ConnectNamedPipe(h, o)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case errors.Is(err, windows.ERROR_IO_PENDING):
		var n uint32
		return windows.GetOverlappedResult(h, o, &n, true)
	}
	return err
}

// pair returns a connected client and server.
func pair(t *testing.T) (client net.Conn, server *pipeConn) {
	t.Helper()
	s := newPipeServer(t, 1)
	h := s.listen()
	accepted := make(chan *pipeConn, 1)
	go func() {
		c, err := s.accept(h)
		if err != nil {
			t.Error(err)
		}
		accepted <- c
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := dialPipe(ctx, s.name)
	if err != nil {
		t.Fatal(err)
	}
	server = <-accepted
	if server == nil {
		t.FailNow()
	}
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

// result runs f in the background and returns its error on a channel.
func result(f func() error) chan error {
	ch := make(chan error, 1)
	go func() { ch <- f() }()
	return ch
}

func waitErr(t *testing.T, ch chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", what)
		return nil
	}
}

func TestPipeReadsAndWritesBothWays(t *testing.T) {
	client, server := pair(t)
	if _, err := client.Write([]byte("hello core\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(server).ReadString('\n')
	if err != nil || line != "hello core\n" {
		t.Fatalf("server read %q, %v", line, err)
	}
	if _, err := server.Write([]byte("hello host\n")); err != nil {
		t.Fatal(err)
	}
	line, err = bufio.NewReader(client).ReadString('\n')
	if err != nil || line != "hello host\n" {
		t.Fatalf("client read %q, %v", line, err)
	}
	if client.RemoteAddr().Network() != "pipe" || !strings.HasPrefix(client.RemoteAddr().String(), `\\.\pipe\`) {
		t.Fatalf("RemoteAddr = %v", client.RemoteAddr())
	}
}

func TestPipeReadAndWriteAtOnce(t *testing.T) {
	client, server := pair(t)
	// A read in flight must not hold up a write on the same connection.
	read := result(func() error {
		buf := make([]byte, 4)
		_, err := io.ReadFull(client, buf)
		return err
	})
	time.Sleep(20 * time.Millisecond)
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("server read %q, %v", buf, err)
	}
	server.Write([]byte("pong"))
	if err := waitErr(t, read, "Read"); err != nil {
		t.Fatal(err)
	}
}

func TestPipeEOFWhenTheServerCloses(t *testing.T) {
	client, server := pair(t)
	read := result(func() error {
		_, err := client.Read(make([]byte, 16))
		return err
	})
	time.Sleep(20 * time.Millisecond)
	server.Close()
	if err := waitErr(t, read, "Read"); !errors.Is(err, io.EOF) {
		t.Fatalf("Read after the server closed: %v, want EOF", err)
	}
}

func TestPipeReadDeadlineInterruptsARead(t *testing.T) {
	client, server := pair(t)
	read := result(func() error {
		_, err := client.Read(make([]byte, 16))
		return err
	})
	time.Sleep(20 * time.Millisecond)
	client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	err := waitErr(t, read, "Read")
	var ne net.Error
	if !errors.Is(err, os.ErrDeadlineExceeded) || !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("Read past its deadline: %v", err)
	}
	// Until the deadline moves, reads fail at once.
	if _, err := client.Read(make([]byte, 16)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read with the deadline passed: %v", err)
	}
	client.SetReadDeadline(time.Time{})
	server.Write([]byte("after"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "after" {
		t.Fatalf("Read after clearing the deadline: %q, %v", buf, err)
	}
}

func TestPipePastDeadlineInterruptsARead(t *testing.T) {
	// The handshake ends a read this way when its context ends.
	client, _ := pair(t)
	read := result(func() error {
		_, err := client.Read(make([]byte, 16))
		return err
	})
	time.Sleep(20 * time.Millisecond)
	client.SetDeadline(time.Unix(1, 0))
	if err := waitErr(t, read, "Read"); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Read after a past deadline: %v", err)
	}
}

func TestPipeWriteDeadlineInterruptsAStalledWrite(t *testing.T) {
	client, _ := pair(t)
	// The server never reads, so the write stalls once the pipe's buffer
	// is full.
	client.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	write := result(func() error {
		_, err := client.Write(make([]byte, 4<<20))
		return err
	})
	if err := waitErr(t, write, "Write"); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("stalled Write past its deadline: %v", err)
	}
}

func TestPipeCloseInterruptsARead(t *testing.T) {
	client, _ := pair(t)
	read := result(func() error {
		_, err := client.Read(make([]byte, 16))
		return err
	})
	time.Sleep(20 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitErr(t, read, "Read"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read after Close: %v, want net.ErrClosed", err)
	}
	if _, err := client.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Write after Close: %v, want net.ErrClosed", err)
	}
	if err := client.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second Close: %v", err)
	}
}

func TestPipeDialWithNoServer(t *testing.T) {
	_, err := dialPipe(context.Background(), testPipeName(t))
	if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		t.Fatalf("dial with no server: %v, want ERROR_FILE_NOT_FOUND", err)
	}
}

func TestPipeDialWaitsWhileBusy(t *testing.T) {
	s := newPipeServer(t, 1)
	h := s.listen()
	t.Cleanup(func() { windows.CloseHandle(h) })
	connected := result(func() error { return connectInstance(h) })
	first, err := dialPipe(context.Background(), s.name)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitErr(t, connected, "connect"); err != nil {
		t.Fatal(err)
	}

	// The only instance is taken: dialling waits, bounded by ctx.
	short, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := dialPipe(short, s.name); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial while busy: %v, want DeadlineExceeded", err)
	}
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("dial while busy gave up after %v", waited)
	}

	// Free the instance and listen on it again: a waiting dial connects.
	// The instance stays, so the name never disappears in between.
	again := result(func() error {
		time.Sleep(100 * time.Millisecond)
		first.Close()
		if err := windows.DisconnectNamedPipe(h); err != nil {
			return err
		}
		return connectInstance(h)
	})
	ctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	second, err := dialPipe(ctx, s.name)
	if err != nil {
		t.Fatalf("dial once the instance is free: %v", err)
	}
	second.Close()
	if err := waitErr(t, again, "connect"); err != nil {
		t.Fatal(err)
	}
}

// serveHello answers the client's hello line on conn with reply, then
// keeps reading until the connection ends. An empty reply never answers.
func serveHello(conn net.Conn, reply string) {
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var hello map[string]any
	if json.Unmarshal(line, &hello) != nil || hello["method"] != "hello" {
		conn.Close()
		return
	}
	if reply != "" {
		conn.Write([]byte(reply + "\n"))
	}
	io.Copy(io.Discard, r)
}

func TestCoreDialsItsPipe(t *testing.T) {
	s := newPipeServer(t, 1)
	h := s.listen()
	go func() {
		c, err := s.accept(h)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		serveHello(c, helloReply(Generation))
	}()
	core := Core{Socket: s.name}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := core.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Hello.Generation != Generation || !c.Hello.Framed {
		t.Fatalf("Hello = %+v", c.Hello)
	}
}

func TestCoreDialHandshakeHonorsContextOverAPipe(t *testing.T) {
	s := newPipeServer(t, 1)
	h := s.listen()
	go func() {
		c, err := s.accept(h)
		if err != nil {
			return
		}
		defer c.Close()
		serveHello(c, "")
	}()
	core := Core{Socket: s.name}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := core.Dial(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial with a silent core: %v, want DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("Dial took %v after its deadline", took)
	}
}
