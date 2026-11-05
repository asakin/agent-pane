//go:build unix

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-pane/internal/scrollback"
	"github.com/aymanbagabas/go-pty"
)

// fakeExtension answers the host's page.* requests the way the side panel does.
type fakeExtension struct {
	mu      sync.Mutex
	tab     pageTab
	choice  string
	prompts []map[string]any
	reads   []map[string]any
	content string
	readErr error
}

func (f *fakeExtension) call(method string, params any, _ time.Duration) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := params.(map[string]any)
	var out any
	switch method {
	case "page.resolve":
		out = f.tab
	case "page.approve":
		f.prompts = append(f.prompts, p)
		out = map[string]string{"choice": f.choice}
	case "page.read":
		f.reads = append(f.reads, p)
		if f.readErr != nil {
			return nil, f.readErr
		}
		out = map[string]string{"content": f.content}
	default:
		return nil, fmt.Errorf("unexpected %s", method)
	}
	return json.Marshal(out)
}

func (f *fakeExtension) set(fn func(f *fakeExtension)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeExtension) promptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prompts)
}

// seen returns copies of the prompts and reads so far. The race detector
// cannot see the ordering the socket gives, so tests read under the lock.
func (f *fakeExtension) seen() (prompts, reads []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.prompts...), append([]map[string]any(nil), f.reads...)
}

// pageSession is a session with a live page channel and no shell; the test
// holds the descriptor a shell would inherit.
func pageSession(t *testing.T, ext *fakeExtension) (*SessionManager, *Session, string) {
	t.Helper()
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if ext != nil {
		m.SetExtensionCaller(ext.call)
	}
	host, child, err := newPageChannel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Close() })
	tty, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{ID: "tty1", Pty: tty, Buf: scrollback.New(64), manager: m, Label: "Claude Code", page: host}
	m.sessions[s.ID] = s
	t.Cleanup(func() { _ = m.Destroy(s.ID) })
	go s.servePage(host, m.extensionCaller)
	return m, s, fmt.Sprint(child.Fd())
}

func read(fd, format string) (string, error) {
	return pageClient(fd, pageRequest{Method: "page.read", Format: format})
}

func TestPageReadReturnsContent(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7, Title: "Example", URL: "https://example.com/"}, choice: "tab", content: "hello page"}
	_, _, fd := pageSession(t, ext)
	for _, format := range []string{"info", "selection", "text", "dom", "markdown"} {
		got, err := read(fd, format)
		if err != nil || got != "hello page" {
			t.Fatalf("%s: %q %v", format, got, err)
		}
	}
	_, reads := ext.seen()
	if len(reads) != 5 || reads[4]["format"] != "markdown" || reads[4]["tabId"] != 7 || reads[4]["tty"] != "tty1" {
		t.Fatalf("reads %v", reads)
	}
	if _, err := pageClient(fd, pageRequest{Method: "page.read", Format: "markdown", Full: true}); err != nil {
		t.Fatal(err)
	}
	if _, reads = ext.seen(); reads[5]["full"] != true {
		t.Fatalf("--full was not passed: %v", reads[5])
	}
	if _, err := read(fd, "cookies"); err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unknown format: %v", err)
	}
}

func TestPageFirstReadPromptsAndAllowTabIsRemembered(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7, Title: "Example", URL: "https://example.com/"}, choice: "tab", content: "x"}
	_, _, fd := pageSession(t, ext)
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	if ext.promptCount() != 1 {
		t.Fatalf("prompts %d", ext.promptCount())
	}
	prompts, _ := ext.seen()
	p := prompts[0]
	if p["agent"] != "Claude Code" || p["title"] != "Example" || p["url"] != "https://example.com/" || p["tty"] != "tty1" {
		t.Fatalf("prompt %v", p)
	}
	if _, err := read(fd, "text"); err != nil || ext.promptCount() != 1 {
		t.Fatalf("second read of the same tab prompted again: %d %v", ext.promptCount(), err)
	}
	// Another tab is not covered by "Allow this tab".
	ext.set(func(f *fakeExtension) {
		f.tab = pageTab{TabID: 8, Title: "Other", URL: "https://other.example/"}
		f.choice = "deny"
	})
	if _, err := read(fd, "text"); err == nil || err.Error() != "denied" {
		t.Fatalf("deny: %v", err)
	}
	if prompts, reads := ext.seen(); len(prompts) != 2 || len(reads) != 2 {
		t.Fatalf("prompts %d reads %d", len(prompts), len(reads))
	}
}

func TestPageAllowAllCoversEveryTab(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 1}, choice: "all"}
	_, _, fd := pageSession(t, ext)
	for id := 1; id <= 3; id++ {
		ext.set(func(f *fakeExtension) { f.tab = pageTab{TabID: id} })
		if _, err := read(fd, "info"); err != nil {
			t.Fatal(err)
		}
	}
	if ext.promptCount() != 1 {
		t.Fatalf("prompts %d", ext.promptCount())
	}
}

func TestPageApprovalsBelongToOneTerminal(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all"}
	_, _, fd1 := pageSession(t, ext)
	_, _, fd2 := pageSession(t, ext)
	if _, err := read(fd1, "text"); err != nil {
		t.Fatal(err)
	}
	ext.set(func(f *fakeExtension) { f.choice = "deny" })
	if _, err := read(fd2, "text"); err == nil || err.Error() != "denied" {
		t.Fatalf("approval leaked to another terminal: %v", err)
	}
	if _, err := read(fd1, "text"); err != nil {
		t.Fatalf("first terminal lost its approval: %v", err)
	}
}

func TestPageConcurrentReadsShareOnePrompt(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "tab", content: "ok"}
	_, _, fd := pageSession(t, ext)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := read(fd, "text")
			if err == nil && got != "ok" {
				err = fmt.Errorf("got %q", got)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if ext.promptCount() != 1 {
		t.Fatalf("prompts %d", ext.promptCount())
	}
}

func TestPageClosedTerminalStopsWorking(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all"}
	m, s, fd := pageSession(t, ext)
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := read(fd, "text"); err == nil || err.Error() != errTerminalClosed {
		t.Fatalf("descriptor outlived its terminal: %v", err)
	}
	if s.allowAll || s.allowedTabs != nil {
		t.Fatal("approvals survived the terminal")
	}
}

func TestPageErrors(t *testing.T) {
	t.Run("no descriptor", func(t *testing.T) {
		if _, err := read("", "text"); err == nil || err.Error() != errNotInTerminal {
			t.Fatalf("%v", err)
		}
	})
	t.Run("not an Agent Pane descriptor", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "x")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, fd := range []string{fmt.Sprint(f.Fd()), "999", "abc", "-1"} {
			if _, err := read(fd, "text"); err == nil || err.Error() != errNotPageChannel {
				t.Fatalf("fd %s: %v", fd, err)
			}
		}
	})
	t.Run("extension not connected", func(t *testing.T) {
		_, _, fd := pageSession(t, nil)
		if _, err := read(fd, "text"); err == nil || err.Error() != errNotConnected {
			t.Fatalf("%v", err)
		}
	})
	t.Run("page Chrome will not script", func(t *testing.T) {
		msg := "Chrome does not let extensions read chrome:// pages"
		ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all", readErr: fmt.Errorf("%s", msg)}
		_, _, fd := pageSession(t, ext)
		if _, err := read(fd, "text"); err == nil || err.Error() != msg {
			t.Fatalf("%v", err)
		}
	})
}

// TestPageHelperProcess is the program a terminal runs: it reads the page
// through whatever it inherited and prints the outcome.
func TestPageHelperProcess(t *testing.T) {
	if os.Getenv("PAGE_HELPER") == "" {
		t.Skip()
	}
	got, err := read(os.Getenv(pageFDEnv), "text")
	if err != nil {
		fmt.Printf("ERR[%s]\n", err)
	} else {
		fmt.Printf("OK[%s]\n", got)
	}
	os.Exit(0)
}

func TestPageDescriptorIsInheritedOnlyInsideTheTerminal(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all", content: "from the tab"}
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetExtensionCaller(ext.call)
	s, err := m.Create("Terminal", func(tty pty.Pty, _ string) (*pty.Cmd, error) {
		cmd := tty.Command(os.Args[0], "-test.run=^TestPageHelperProcess$")
		cmd.Env = append(os.Environ(), "PAGE_HELPER=1")
		return cmd, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Destroy(s.ID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.writeMu.Lock()
		got := string(s.Buf.Bytes())
		s.writeMu.Unlock()
		if strings.Contains(got, "OK[from the tab]") {
			break
		}
		if strings.Contains(got, "ERR[") || time.Now().After(deadline) {
			t.Fatalf("terminal output %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The same program started outside a terminal has nothing to read with,
	// even when it copies the variable.
	outside := exec.Command(os.Args[0], "-test.run=^TestPageHelperProcess$")
	outside.Env = append(os.Environ(), "PAGE_HELPER=1", pageFDEnv+"=3")
	got, err := outside.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ERR["+errNotPageChannel+"]") {
		t.Fatalf("outside process: %q", got)
	}
}
