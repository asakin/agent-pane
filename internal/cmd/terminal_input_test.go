package cmd

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-terminal/internal/scrollback"
	"github.com/aymanbagabas/go-pty"
	"github.com/gorilla/websocket"
)

type inputCapturePty struct {
	pty.Pty
	writes chan []byte
}

func (p *inputCapturePty) Write(data []byte) (int, error) {
	p.writes <- append([]byte(nil), data...)
	return len(data), nil
}

func TestWebSocketPreservesTerminalControlBytes(t *testing.T) {
	capture := &inputCapturePty{writes: make(chan []byte, 1)}
	session := &Session{Pty: capture, Buf: scrollback.New(1024)}
	server := httptest.NewServer(HandleWebsocket(session))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{{0}, {0, 0x15, 0x1b, '[', '1', '3', ';', '2', 'u', 0}} {
		if err := conn.WriteMessage(websocket.BinaryMessage, want); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-capture.writes:
			if !bytes.Equal(got, want) {
				t.Fatalf("terminal input changed: got %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("terminal input was not forwarded")
		}
	}
}
