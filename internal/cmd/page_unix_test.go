//go:build unix

package cmd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
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

func (f *fakeExtension) call(_ context.Context, method string, params any) (json.RawMessage, error) {
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
		if want, ok := p["expectUrl"]; ok && want != f.tab.URL {
			return nil, errors.New("the tab navigated after approval")
		}
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
	s, fd := sessionInPane(t, m, "tty1", "")
	return m, s, fd
}

// sessionInPane adds a session like pageSession's to m, started for the pane
// tab paneID the way tty.create starts one.
func sessionInPane(t *testing.T, m *SessionManager, id, paneID string) (*Session, string) {
	t.Helper()
	host, child, err := newPageChannel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Close() })
	tty, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{ID: id, Pty: tty, Buf: scrollback.New(64), manager: m, Label: "Claude Code", page: host}
	m.mu.Lock()
	s.pane = m.paneLocked(paneID)
	m.sessions[s.ID] = s
	m.mu.Unlock()
	t.Cleanup(func() { _ = m.Destroy(s.ID) })
	go s.servePage(host, m.extensionCaller)
	return s, fmt.Sprint(child.Fd())
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
	// The pane tab's grant is not the session's: it stays for a
	// replacement session until the pane tab closes.
	if !s.pane.isAllowed(7) {
		t.Fatal("closing the PTY session revoked the pane tab's grant")
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
	s, err := m.Create("Terminal", "", "", func(tty pty.Pty, _ string) (*pty.Cmd, error) {
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

// blockingExtension holds page.approve until the test answers it.
type blockingExtension struct {
	fakeExtension
	asked   chan struct{}
	answer  chan string
	cancels chan map[string]any
}

func newBlockingExtension() *blockingExtension {
	return &blockingExtension{
		fakeExtension: fakeExtension{tab: pageTab{TabID: 7}, content: "secret"},
		asked:         make(chan struct{}, 8),
		answer:        make(chan string, 8),
		cancels:       make(chan map[string]any, 8),
	}
}

func (b *blockingExtension) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	switch method {
	case "page.approve":
		b.mu.Lock()
		b.prompts = append(b.prompts, params.(map[string]any))
		b.mu.Unlock()
		b.asked <- struct{}{}
		select {
		case choice := <-b.answer:
			return json.Marshal(map[string]string{"choice": choice})
		case <-ctx.Done():
			return nil, errExtensionTimeout
		}
	case "page.cancel":
		b.cancels <- params.(map[string]any)
		return json.Marshal(map[string]any{})
	}
	return b.fakeExtension.call(ctx, method, params)
}

func TestPageClosingDuringApprovalEndsTheRead(t *testing.T) {
	ext := newBlockingExtension()
	m, s, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	errs := make(chan error, 2)
	go func() { _, err := read(fd, "text"); errs <- err }()
	<-ext.asked
	// A second read queues behind the open prompt.
	go func() { _, err := read(fd, "text"); errs <- err }()
	time.Sleep(50 * time.Millisecond)

	if err := m.Destroy(s.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-errs:
			if err == nil || err.Error() != errTerminalClosed {
				t.Fatalf("read after close: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a pending read outlived its terminal")
		}
	}
	// The late answer changes nothing.
	ext.answer <- "all"
	time.Sleep(50 * time.Millisecond)
	if s.pane.isAllowed(7) {
		t.Fatal("a late answer granted access to a closed terminal")
	}
	if _, reads := ext.seen(); len(reads) != 0 {
		t.Fatalf("the page was read after close: %v", reads)
	}
	if prompts, _ := ext.seen(); len(prompts) != 1 {
		t.Fatalf("prompts %d", len(prompts))
	}
}

func TestPageGrantAfterCloseIsRefused(t *testing.T) {
	p := newPaneTab()
	p.close()
	if p.grant(7, "all") || p.isAllowed(7) {
		t.Fatal("grant on a closed pane tab")
	}
}

func TestPageApprovalTimeoutCancelsThePromptAndFreesTheTurn(t *testing.T) {
	defer func(old time.Duration) { pageApproveTimeout = old }(pageApproveTimeout)
	pageApproveTimeout = 100 * time.Millisecond
	ext := newBlockingExtension()
	m, _, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)

	if _, err := read(fd, "text"); err == nil || err.Error() != errApprovalTimeout {
		t.Fatalf("unanswered prompt: %v", err)
	}
	<-ext.asked
	select {
	case c := <-ext.cancels:
		prompts, _ := ext.seen()
		if c["id"] == "" || c["id"] != prompts[0]["id"] || c["tty"] != "tty1" {
			t.Fatalf("cancel %v for prompt %v", c, prompts[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the expired prompt was not cancelled")
	}
	// The next read gets its own prompt and is not held up.
	ext.answer <- "tab"
	if got, err := read(fd, "text"); err != nil || got != "secret" {
		t.Fatalf("read after an expired prompt: %q %v", got, err)
	}
}

func TestPageQueuedReadGivesUpAtItsDeadline(t *testing.T) {
	defer func(a, r, w time.Duration) { pageApproveTimeout, pageReadTimeout, pageResolveTimeout = a, r, w }(pageApproveTimeout, pageReadTimeout, pageResolveTimeout)
	pageApproveTimeout, pageReadTimeout, pageResolveTimeout = 300*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
	ext := newBlockingExtension()
	m, _, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	start := time.Now()
	errs := make(chan error, 3)
	for range 3 {
		go func() { _, err := read(fd, "text"); errs <- err }()
	}
	for range 3 {
		if err := <-errs; err == nil {
			t.Fatal("unanswered read succeeded")
		}
	}
	// Each read is bounded by one request's time, not one prompt each in turn.
	if took := time.Since(start); took >= 2*pageApproveTimeout {
		t.Fatalf("queued reads took %v", took)
	}
}

func TestPageReplySocketDeadlinesWork(t *testing.T) {
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, err := pollableFile(pair[0], "a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := pollableFile(pair[1], "b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := a.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	if _, err := a.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read past deadline: %v", err)
	}

	// A reader that never reads cannot hold the host's writer.
	defer func(old time.Duration) { pageReplyWriteTimeout = old }(pageReplyWriteTimeout)
	pageReplyWriteTimeout = 100 * time.Millisecond
	done := make(chan struct{})
	go func() {
		writePageReply(b, pageReply{Content: strings.Repeat("x", 8<<20)})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writing a reply nobody reads did not time out")
	}
}

func TestPaneTabGrantSurvivesReplacementSessions(t *testing.T) {
	// Terminal pane tab -> OpenCode -> approve -> OpenCode exits -> the panel
	// starts Claude Code in the same pane tab, which keeps the grant.
	ext := &fakeExtension{tab: pageTab{TabID: 7, URL: "https://a.example/"}, choice: "all", content: "x"}
	m, first, fd := pageSession(t, ext)
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	paneID := first.pane.id
	if err := m.Destroy(first.ID); err != nil {
		t.Fatal(err)
	}
	ext.set(func(f *fakeExtension) { f.choice = "deny" })
	second, fd2 := sessionInPane(t, m, "tty2", paneID)
	if second.pane != first.pane {
		t.Fatal("the replacement session got a new pane tab")
	}
	if _, err := read(fd2, "text"); err != nil {
		t.Fatalf("replacement session lost the pane tab's grant: %v", err)
	}
	if ext.promptCount() != 1 {
		t.Fatalf("prompts %d", ext.promptCount())
	}
	// The old descriptor stays dead.
	if _, err := read(fd, "text"); err == nil || err.Error() != errTerminalClosed {
		t.Fatalf("old descriptor: %v", err)
	}
}

func TestPaneTabsHaveIndependentGrants(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all"}
	m, first, fd1 := pageSession(t, ext)
	if _, err := read(fd1, "text"); err != nil {
		t.Fatal(err)
	}
	ext.set(func(f *fakeExtension) { f.choice = "deny" })
	// A new pane tab running the same coding agent asks for itself.
	_, fd2 := sessionInPane(t, m, "tty2", "")
	if _, err := read(fd2, "text"); err == nil || err.Error() != errDenied {
		t.Fatalf("new pane tab: %v", err)
	}
	// Naming a pane tab whose session is alive does not join it, and an
	// id the host never made gets nothing.
	for i, id := range []string{first.pane.id, "pane-made-up"} {
		s, fd := sessionInPane(t, m, fmt.Sprintf("tty%d", 3+i), id)
		if s.pane == first.pane {
			t.Fatalf("%s: joined a live pane tab", id)
		}
		if _, err := read(fd, "text"); err == nil || err.Error() != errDenied {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := read(fd1, "text"); err != nil {
		t.Fatalf("first pane tab lost its grant: %v", err)
	}
}

func TestClosingPaneTabRevokesItsGrant(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all"}
	m, first, fd := pageSession(t, ext)
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	paneID := first.pane.id
	m.ClosePane(paneID)
	_ = m.Destroy(first.ID)
	if first.pane.isAllowed(7) {
		t.Fatal("closed pane tab kept its grant")
	}
	ext.set(func(f *fakeExtension) { f.choice = "deny" })
	s, fd2 := sessionInPane(t, m, "tty2", paneID)
	if s.pane == first.pane {
		t.Fatal("a closed pane tab was reused")
	}
	if _, err := read(fd2, "text"); err == nil || err.Error() != errDenied {
		t.Fatalf("after close: %v", err)
	}
}

func TestClosingPaneTabDuringApprovalRefusesTheLateAnswer(t *testing.T) {
	ext := newBlockingExtension()
	m, s, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	errs := make(chan error, 1)
	go func() { _, err := read(fd, "text"); errs <- err }()
	<-ext.asked
	m.ClosePane(s.pane.id)
	ext.answer <- "all"
	if err := <-errs; err == nil {
		t.Fatal("read succeeded after its pane tab closed")
	}
	if s.pane.isAllowed(7) {
		t.Fatal("a late answer granted a closed pane tab")
	}
	if _, reads := ext.seen(); len(reads) != 0 {
		t.Fatalf("read after close: %v", reads)
	}
}

func TestPageReadNamesTheApprovedDocument(t *testing.T) {
	ext := &fakeExtension{tab: pageTab{TabID: 7, URL: "https://a.example/"}, choice: "tab", content: "x"}
	_, _, fd := pageSession(t, ext)
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	// Covered by the grant: later navigation is the grant's to cover.
	ext.set(func(f *fakeExtension) { f.tab.URL = "https://b.example/" })
	if _, err := read(fd, "text"); err != nil {
		t.Fatal(err)
	}
	_, reads := ext.seen()
	if reads[0]["expectUrl"] != "https://a.example/" {
		t.Fatalf("prompted read did not name the approved URL: %v", reads[0])
	}
	if _, ok := reads[1]["expectUrl"]; ok {
		t.Fatalf("covered read was pinned to a URL: %v", reads[1])
	}
}

func TestPageReadsInFlightAreBounded(t *testing.T) {
	defer func(old int) { maxSessionPageReads = old }(maxSessionPageReads)
	maxSessionPageReads = 2
	ext := newBlockingExtension()
	m, s, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	errs := make(chan error, 4)
	for range 2 {
		go func() { _, err := read(fd, "text"); errs <- err }()
	}
	<-ext.asked
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.replies) == 2 })
	// Over the limit: refused at once, its descriptor closed.
	if _, err := read(fd, "text"); err == nil || err.Error() != errTooManyReads {
		t.Fatalf("over the limit: %v", err)
	}
	ext.answer <- "tab"
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.replies) == 0 })
	if n := m.pageReads.Load(); n != 0 {
		t.Fatalf("host still counts %d reads", n)
	}
	if _, err := read(fd, "text"); err != nil {
		t.Fatalf("after the burst: %v", err)
	}
}

func TestPageReadEndsWhenTheClientGoesAway(t *testing.T) {
	ext := newBlockingExtension()
	m, s, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	// A client that sends a request and exits, as when the person presses
	// Ctrl-C while the prompt is up.
	child := exec.Command(os.Args[0], "-test.run=^TestPageHelperProcess$")
	child.Env = append(os.Environ(), "PAGE_HELPER=1", pageFDEnv+"=3")
	dup, err := syscall.Dup(mustAtoi(t, fd))
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(dup), "page")
	defer f.Close()
	child.ExtraFiles = []*os.File{f}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	<-ext.asked
	_ = child.Process.Kill()
	_ = child.Wait()
	select {
	case <-ext.cancels:
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt outlived the client that asked")
	}
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.replies) == 0 })
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscan(s, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCreateJoinsTheDeadSessionsPaneTab(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	start := func(pane, prog string) *Session {
		s, err := m.Create("Terminal", pane, "", func(tty pty.Pty, _ string) (*pty.Cmd, error) {
			return tty.Command("/bin/sh", "-c", prog), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Destroy(s.ID) })
		return s
	}
	first := start("", "exit 0")
	if !first.pane.grant(7, "all") {
		t.Fatal("grant refused")
	}
	waitFor(t, func() bool { _, ok := m.Get(first.ID); return !ok })

	// tty.create and tty.attach name the pane tab to the extension.
	second := start(first.pane.id, "sleep 30")
	if second.pane != first.pane || !second.pane.isAllowed(7) {
		t.Fatal("the replacement session did not keep the pane tab's grant")
	}
	ep, err := m.endpoint(second.ID)
	if err != nil || ep["pane"] != first.pane.id {
		t.Fatalf("endpoint %v %v", ep, err)
	}
	// While second is alive, another session cannot join its pane tab.
	if third := start(first.pane.id, "sleep 30"); third.pane == first.pane {
		t.Fatal("a session joined a pane tab that has a live session")
	}
}

func TestClosingASessionReleasesItsHostWideReads(t *testing.T) {
	ext := newBlockingExtension()
	m, s, fd := pageSession(t, nil)
	m.SetExtensionCaller(ext.call)
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := read(fd, "text"); errs <- err }()
	}
	<-ext.asked
	waitFor(t, func() bool { return m.pageReads.Load() == 2 })
	if err := m.Destroy(s.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		<-errs
	}
	waitFor(t, func() bool { return m.pageReads.Load() == 0 })
}

func TestNativeCallerNamesItsExpiry(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	h := NewMessagingHost(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "test")
	h.SetOutput(w)
	call := nativeCaller(h)
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	go func() { _, _ = call(ctx, "page.approve", map[string]any{"tty": "a"}) }()
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	var req struct {
		Params struct {
			TTY       string `json:"tty"`
			ExpiresAt int64  `json:"expiresAt"`
		} `json:"params"`
	}
	if err := json.Unmarshal(buf, &req); err != nil {
		t.Fatal(err)
	}
	if req.Params.TTY != "a" || req.Params.ExpiresAt != deadline.UnixMilli() {
		t.Fatalf("params %+v", req.Params)
	}
}
