//go:build unix

package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Socket paths are short-limited, and tests must not write into the
	// user's data folder.
	dir, err := os.MkdirTemp("", "ap")
	if err != nil {
		panic(err)
	}
	pageSocketDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// TestPageSocketHelperProcess reads the page the way a coding agent's command
// tool does: AGENT_PANE_FD is set, but fd 3 was not passed on.
func TestPageSocketHelperProcess(t *testing.T) {
	if os.Getenv("PAGE_SOCKET_HELPER") == "" {
		t.Skip()
	}
	got, err := readPage(os.Getenv(pageFDEnv), os.Getenv(envSocket), pageRequest{Method: "page.read", Format: "text"})
	if err != nil {
		fmt.Printf("ERR[%s]\n", err)
	} else {
		fmt.Printf("OK[%s]\n", got)
	}
	os.Exit(0)
}

func waitOutput(t *testing.T, s *Session, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := sessionOutput(s)
		if strings.Contains(got, want) {
			return got
		}
		if strings.Contains(got, "ERR[") || time.Now().After(deadline) {
			t.Fatalf("terminal output %q", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPageSocketServesOnlyTheSessionsProcesses(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	ext := &fakeExtension{tab: pageTab{TabID: 7}, choice: "all", content: "from the tab"}
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetExtensionCaller(ext.call)
	// A shell that starts the reader with fd 3 closed, as Claude Code's Bash
	// tool does, prints the socket path, and waits.
	prog := `printf 'SOCK[%s]\n' "$AGENT_PANE_SOCKET"; "$0" -test.run='^TestPageSocketHelperProcess$' 3>&-; sleep 30`
	s, err := m.Create("Terminal", "", "claude", func() *terminalCommand {
		cmd := newTerminalCommand("/bin/sh", "-c", prog, os.Args[0])
		cmd.Env = append(os.Environ(), "PAGE_SOCKET_HELPER=1", envSocket+"=/stale/path.sock")
		return cmd
	}())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Destroy(s.ID)
	out := waitOutput(t, s, "OK[from the tab]")

	start := strings.Index(out, "SOCK[") + len("SOCK[")
	path := out[start : start+strings.Index(out[start:], "]")]
	if !strings.HasPrefix(path, pageSocketDir+"/") {
		t.Fatalf("socket %q is not the session's", path)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v %v", info, err)
	}

	// The same path from a process the session did not start is refused.
	outside := exec.Command(os.Args[0], "-test.run=^TestPageSocketHelperProcess$")
	outside.Env = append(os.Environ(), "PAGE_SOCKET_HELPER=1", envSocket+"="+path)
	got, err := outside.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "ERR["+errNotSessionProcess+"]") {
		t.Fatalf("outside process: %q", got)
	}
	if _, reads := ext.seen(); len(reads) != 1 {
		t.Fatalf("reads %v", reads)
	}

	// Closing the session removes the socket.
	if err := m.Destroy(s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket outlived the session: %v", err)
	}
	if _, err := readPage("", path, pageRequest{Method: "page.read", Format: "text"}); err == nil || err.Error() != errTerminalClosed {
		t.Fatalf("read after close: %v", err)
	}
}

func TestStartingSessionIsNotLive(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	checked := make(chan struct{})
	release := make(chan struct{})
	var id string
	go func() {
		defer close(checked)
		// Create holds here once the session is registered and before its
		// process starts.
		<-release
		m.mu.RLock()
		for k := range m.sessions {
			id = k
		}
		m.mu.RUnlock()
		if ids := m.List(); len(ids) != 0 {
			t.Errorf("listed while starting: %v", ids)
		}
		if _, ok := m.Get(id); ok {
			t.Error("Get returned a session still starting")
		}
		if err := m.Destroy(id); err == nil {
			t.Error("Destroy ended a session still starting")
		}
	}()
	hook := startHook
	startHook = func() { close(release); <-checked }
	t.Cleanup(func() { startHook = hook })
	s, err := m.Create("Terminal", "", "", newTerminalCommand("/bin/sh", "-c", "sleep 30"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Destroy(s.ID)
	if ids := m.List(); len(ids) != 1 || ids[0] != s.ID {
		t.Fatalf("started session not listed: %v", ids)
	}
	if _, ok := m.Get(s.ID); !ok {
		t.Fatal("started session not found")
	}
}
