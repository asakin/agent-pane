package cmd

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"agent-pane/internal/scrollback"
	"github.com/gorilla/websocket"
)

type wsHarness struct {
	sessions *SessionManager
	server   *httptest.Server
	clock    time.Time
}

func newWSHarness(t *testing.T, ids ...string) *wsHarness {
	t.Helper()
	h := &wsHarness{clock: time.Unix(1_700_000_000, 0)}
	h.sessions = NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.sessions.now = func() time.Time { return h.clock }
	for _, id := range ids {
		s := &Session{ID: id, Buf: scrollback.New(1024), manager: h.sessions}
		_, _ = s.Buf.Write([]byte("replay-" + id))
		h.sessions.sessions[id] = s
	}
	h.server = httptest.NewServer(NewWebSocketHandler(h.sessions))
	t.Cleanup(h.server.Close)
	return h
}

func (h *wsHarness) url(id, token string) string {
	u := "ws" + strings.TrimPrefix(h.server.URL, "http") + "/tty/" + id
	if token != "" {
		u += "?token=" + url.QueryEscape(token)
	}
	return u
}

func (h *wsHarness) token(t *testing.T, id string) string {
	t.Helper()
	ep, err := h.sessions.endpoint(id)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ep["url"])
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/tty/"+id {
		t.Fatalf("endpoint path %q", u.Path)
	}
	return u.Query().Get("token")
}

func (h *wsHarness) dial(id, token, origin string) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(h.url(id, token), header)
}

var extOrigin = extensionOrigins[0]

func expectRefused(t *testing.T, h *wsHarness, id, token, origin string) {
	t.Helper()
	conn, resp, err := h.dial(id, token, origin)
	if err == nil {
		conn.Close()
		t.Fatal("connection was accepted")
	}
	if resp == nil {
		t.Fatalf("no HTTP response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), id) || (token != "" && strings.Contains(string(body), token)) {
		t.Fatalf("refusal echoes the id or token: %q", body)
	}
}

func expectReplay(t *testing.T, conn *websocket.Conn, id string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage || string(data) != "replay-"+id {
		t.Fatalf("replay %d %q", kind, data)
	}
}

func TestWebSocketAcceptsValidToken(t *testing.T) {
	h := newWSHarness(t, "aaa")
	conn, _, err := h.dial("aaa", h.token(t, "aaa"), extOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	expectReplay(t, conn, "aaa")
}

func TestWebSocketRejectsOrigin(t *testing.T) {
	h := newWSHarness(t, "aaa")
	for _, origin := range []string{"", "https://evil.example", "chrome-extension://abcdefghijklmnopabcdefghijklmnop", "null"} {
		expectRefused(t, h, "aaa", h.token(t, "aaa"), origin)
	}
}

func TestWebSocketRejectsBadTokens(t *testing.T) {
	h := newWSHarness(t, "aaa", "bbb")

	t.Run("missing", func(t *testing.T) {
		expectRefused(t, h, "aaa", "", extOrigin)
	})
	t.Run("wrong", func(t *testing.T) {
		h.token(t, "aaa")
		expectRefused(t, h, "aaa", strings.Repeat("A", 43), extOrigin)
		expectRefused(t, h, "aaa", "not-a-token", extOrigin)
	})
	t.Run("reused", func(t *testing.T) {
		token := h.token(t, "aaa")
		conn, _, err := h.dial("aaa", token, extOrigin)
		if err != nil {
			t.Fatal(err)
		}
		expectReplay(t, conn, "aaa")
		conn.Close()
		expectRefused(t, h, "aaa", token, extOrigin)
	})
	t.Run("expired", func(t *testing.T) {
		token := h.token(t, "aaa")
		h.clock = h.clock.Add(attachTokenTTL)
		expectRefused(t, h, "aaa", token, extOrigin)
	})
	t.Run("other tty", func(t *testing.T) {
		token := h.token(t, "bbb")
		expectRefused(t, h, "aaa", token, extOrigin)
		// The token was spent on the failed attempt.
		expectRefused(t, h, "bbb", token, extOrigin)
	})
	t.Run("origin refusal leaves the token unused", func(t *testing.T) {
		token := h.token(t, "aaa")
		expectRefused(t, h, "aaa", token, "https://evil.example")
		conn, _, err := h.dial("aaa", token, extOrigin)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		expectReplay(t, conn, "aaa")
	})
}

func TestWebSocketTokenForUnknownTTY(t *testing.T) {
	h := newWSHarness(t)
	token := h.token(t, "zzz")
	expectRefused(t, h, "zzz", token, extOrigin)
}
