package jsonrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// stalledHost is a host whose stdout nobody reads, as when the browser stops
// draining the native-messaging pipe.
func stalledHost(t *testing.T) (*Host, *io.PipeReader) {
	t.Helper()
	r, w := io.Pipe()
	h := NewHost(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.out = w
	t.Cleanup(func() { r.Close() })
	return h, r
}

func TestSendRequestGivesUpOnAStalledPipe(t *testing.T) {
	h, _ := stalledHost(t)
	// The first request blocks the writer; the rest queue behind it, and
	// more than the queue holds wait to enter it. Every one gives up at
	// its own deadline.
	start := time.Now()
	errs := make(chan error, 100)
	for i := range 100 {
		go func() {
			_, err := h.SendRequestTimeout(JSONRPCRequest{JSONRPCVersion: "2.0", ID: strings.Repeat("x", i+1), Method: "page.read"}, 100*time.Millisecond)
			errs <- err
		}()
	}
	for range 100 {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("a request nobody received succeeded")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a request outlived its deadline behind a stalled pipe")
		}
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("took %v", took)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clientChannels) != 0 {
		t.Fatalf("%d requests still wait for answers", len(h.clientChannels))
	}
}

func TestWithdrawnRequestsAreNotWritten(t *testing.T) {
	h, r := stalledHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// "first" holds the writer until the pipe is read; "second" is queued
	// behind it and withdrawn when its context ends.
	go func() {
		_, _ = h.SendRequestContext(context.Background(), JSONRPCRequest{JSONRPCVersion: "2.0", ID: "first", Method: "m"})
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := h.SendRequestContext(ctx, JSONRPCRequest{JSONRPCVersion: "2.0", ID: "second", Method: "m"}); err == nil {
		t.Fatal("withdrawn request succeeded")
	}
	go func() { _ = h.writeMessage(JSONRPCResponse{JSONRPC: "2.0", ID: "third"}) }()

	var ids []string
	for range 2 {
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
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if ids[0] != "first" || ids[1] != "third" {
		t.Fatalf("written %v", ids)
	}
}

func TestNotifyIsWrittenAsARequestWithNoID(t *testing.T) {
	r, w := io.Pipe()
	h := NewHost(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.out = w
	t.Cleanup(func() { r.Close() })
	// The sender is gone by the time the writer gets to it.
	ctx, cancel := context.WithCancel(context.Background())
	err := h.Notify(ctx, "tty.event", map[string]string{"tty": "a"})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(buf, &msg); err != nil {
		t.Fatal(err)
	}
	if _, ok := msg["id"]; ok || msg["method"] != "tty.event" || msg["params"].(map[string]any)["tty"] != "a" {
		t.Fatalf("notification %s", buf)
	}
}

func TestNotifyDoesNotWaitOnAStalledPipe(t *testing.T) {
	h, _ := stalledHost(t)
	// Enough to fill the queue behind the write the stalled pipe holds.
	start := time.Now()
	for range 100 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_ = h.Notify(ctx, "tty.event", nil)
		cancel()
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v", took)
	}
}
