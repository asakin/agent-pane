//go:build unix

package jsonrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// fullPipeHost is a host writing to a pipe that is already full, as when the
// browser stops draining stdout. It returns the read end and how many filler
// bytes sit in the pipe ahead of anything the host writes.
func fullPipeHost(t *testing.T) (*Host, *os.File, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	filled := 0
	chunk := make([]byte, 4096)
	for {
		_ = w.SetWriteDeadline(time.Now().Add(20 * time.Millisecond))
		n, err := w.Write(chunk)
		filled += n
		if err != nil {
			break
		}
	}
	_ = w.SetWriteDeadline(time.Time{})
	h := NewHost(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.out = w
	return h, r, filled
}

func hookWriteStarted(h *Host) <-chan struct{} {
	started := make(chan struct{}, 8)
	h.writeStarted = func() { started <- struct{}{} }
	return started
}

func readFrameID(t *testing.T, r io.Reader) string {
	t.Helper()
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("frame is not a message: %v", err)
	}
	return m.ID
}

func TestCancelledActiveWriteIsNotDelivered(t *testing.T) {
	h, r, filled := fullPipeHost(t)
	started := hookWriteStarted(h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.SendRequestContext(ctx, JSONRPCRequest{JSONRPCVersion: "2.0", ID: "stale", Method: "page.approve"})
		done <- err
	}()
	// The writer has taken the request and is blocked on the full pipe.
	<-started
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	// The writer is free again: the next message goes straight behind it.
	next := make(chan error, 1)
	go func() { next <- h.writeMessage(JSONRPCResponse{JSONRPC: "2.0", ID: "next"}) }()
	<-started

	// The browser starts draining the pipe again.
	time.Sleep(50 * time.Millisecond)
	if _, err := io.CopyN(io.Discard, r, int64(filled)); err != nil {
		t.Fatal(err)
	}
	if id := readFrameID(t, r); id != "next" {
		t.Fatalf("delivered %q after the pipe recovered", id)
	}
	if err := <-next; err != nil {
		t.Fatal(err)
	}
}

func TestPartlyWrittenFrameIsFinished(t *testing.T) {
	h, r, filled := fullPipeHost(t)
	started := hookWriteStarted(h)
	// Make room for part of a frame larger than the room. A pipe frees
	// space a page at a time.
	if _, err := io.CopyN(io.Discard, r, 4096); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.SendRequestContext(ctx, JSONRPCRequest{
			JSONRPCVersion: "2.0", ID: "big", Method: "m",
			Params: json.RawMessage(`"` + strings.Repeat("x", 8192) + `"`),
		})
		done <- err
	}()
	<-started
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	go func() { _ = h.writeMessage(JSONRPCResponse{JSONRPC: "2.0", ID: "next"}) }()
	if _, err := io.CopyN(io.Discard, r, int64(filled-4096)); err != nil {
		t.Fatal(err)
	}
	// Framing holds: the started frame arrives whole, then the next one.
	if id := readFrameID(t, r); id != "big" {
		t.Fatalf("first frame %q", id)
	}
	if id := readFrameID(t, r); id != "next" {
		t.Fatalf("second frame %q", id)
	}
}

func TestOversizedMessagesAreRefused(t *testing.T) {
	h, _ := stalledHost(t)
	_, err := h.SendRequestContext(context.Background(), JSONRPCRequest{
		JSONRPCVersion: "2.0", ID: "huge", Method: "m",
		Params: json.RawMessage(`"` + strings.Repeat("x", maxOutgoingMessage) + `"`),
	})
	if !errors.Is(err, errMessageTooLarge) {
		t.Fatalf("got %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clientChannels) != 0 {
		t.Fatal("refused request still waits for an answer")
	}
}
