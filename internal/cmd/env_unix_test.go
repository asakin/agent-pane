//go:build unix

package cmd

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
)

func TestWithSessionEnvReplacesReservedKeys(t *testing.T) {
	base := []string{
		"PATH=/user/bin:/bin",
		"AGENT_PANE=1",
		"AGENT_PANE_TAB_ID=tab-stale",
		"AGENT_PANE_SESSION_ID=session-stale",
		"AGENT_PANE_AGENT=claude",
		"AGENT_PANE_FD=9",
		"AGENT_PANE_SKILL_PATH=/elsewhere/SKILL.md",
		"TERM_PROGRAM=iTerm.app",
		"TERM_PROGRAM_VERSION=3.5",
		"EDITOR=vim",
	}
	got := withSessionEnv(base, map[string]string{
		envPresence:    "1",
		envTermProgram: "agent-pane",
		envTabID:       "tab-new",
		envSessionID:   "session-new",
	})
	env := map[string][]string{}
	for _, e := range got {
		k, v, _ := strings.Cut(e, "=")
		env[k] = append(env[k], v)
	}
	want := map[string]string{
		"PATH":                  "/user/bin:/bin",
		"EDITOR":                "vim",
		"AGENT_PANE":            "1",
		"TERM_PROGRAM":          "agent-pane",
		"AGENT_PANE_TAB_ID":     "tab-new",
		"AGENT_PANE_SESSION_ID": "session-new",
	}
	for k, v := range want {
		if len(env[k]) != 1 || env[k][0] != v {
			t.Errorf("%s = %v, want %q", k, env[k], v)
		}
	}
	// Keys this session has no value for are removed, not inherited.
	for _, k := range []string{"AGENT_PANE_AGENT", "AGENT_PANE_FD", "AGENT_PANE_SKILL_PATH", "TERM_PROGRAM_VERSION"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s inherited: %v", k, env[k])
		}
	}
}

func TestBuildPtyCommandKeepsUserEnvButNotReservedKeys(t *testing.T) {
	k.Set("env", map[string]any{"PATH": "/configured/bin", "AGENT_PANE_TAB_ID": "forged", "FOO": "bar"})
	t.Cleanup(func() { k.Delete("env") })
	t.Setenv("AGENT_PANE_SESSION_ID", "inherited")
	tty, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	cmd, err := buildPtyCommand(tty, "", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(withSessionEnv(cmd.Env, map[string]string{envTabID: "tab-real", envSessionID: "session-real"}), "\n") + "\n"
	for _, want := range []string{"PATH=/configured/bin\n", "FOO=bar\n", "AGENT_PANE_TAB_ID=tab-real\n", "AGENT_PANE_SESSION_ID=session-real\n"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"forged", "inherited"} {
		if strings.Contains(env, bad) {
			t.Errorf("reserved key kept %q", bad)
		}
	}
}

// envSession starts a PTY session the way tty.create does, whose program
// prints its Agent Pane variables and then waits, and returns them.
func envSession(t *testing.T, m *SessionManager, pane, agent string, inherited ...string) (*Session, map[string]string) {
	t.Helper()
	prog := `for k in AGENT_PANE TERM_PROGRAM AGENT_PANE_TAB_ID AGENT_PANE_SESSION_ID AGENT_PANE_AGENT AGENT_PANE_FD AGENT_PANE_SKILL_PATH; do eval "printf '%s=%s|\n' $k \"\${$k-<unset>}\""; done; echo END; sleep 30`
	s, err := m.Create("Terminal", pane, agent, func(tty pty.Pty, _ string) (*pty.Cmd, error) {
		cmd := tty.Command("/bin/sh", "-c", prog)
		cmd.Env = append(os.Environ(), inherited...)
		return cmd, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(s.ID) })
	var out string
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.writeMu.Lock()
		out = string(s.Buf.Bytes())
		s.writeMu.Unlock()
		if strings.Contains(out, "END") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal output %q", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(strings.TrimSuffix(line, "|"), "="); ok && strings.HasSuffix(line, "|") {
			env[k] = v
		}
	}
	return s, env
}

func TestSessionEnvIdentities(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	skill := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(skill, []byte("---\nname: agent-pane\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := skillPath
	skillPath = skill
	t.Cleanup(func() { skillPath = old })

	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A host started from inside a pane tab passes its own values down;
	// none of them survive.
	stale := []string{"AGENT_PANE_TAB_ID=tab-stale", "AGENT_PANE_SESSION_ID=session-stale", "AGENT_PANE_AGENT=goose", "AGENT_PANE_FD=9", "TERM_PROGRAM=Apple_Terminal"}
	first, a := envSession(t, m, "", "claude", stale...)
	if a["AGENT_PANE"] != "1" || a["TERM_PROGRAM"] != "agent-pane" || a["AGENT_PANE_AGENT"] != "claude" || a["AGENT_PANE_FD"] != "3" || a["AGENT_PANE_SKILL_PATH"] != skill {
		t.Fatalf("catalog launch env %v", a)
	}
	if !strings.HasPrefix(a["AGENT_PANE_TAB_ID"], "tab-") || !strings.HasPrefix(a["AGENT_PANE_SESSION_ID"], "session-") {
		t.Fatalf("ids %v", a)
	}
	// The pane-tab id the extension holds, and the PTY id the attach token
	// is for, are not handed to the terminal.
	for _, v := range a {
		if strings.Contains(v, first.pane.id) || strings.Contains(v, first.ID) {
			t.Fatalf("host id leaked: %v", a)
		}
	}

	// A plain shell in another pane tab: no agent, different ids.
	_, b := envSession(t, m, "", "")
	if b["AGENT_PANE_AGENT"] != "<unset>" {
		t.Fatalf("shell launch has agent %q", b["AGENT_PANE_AGENT"])
	}
	if b["AGENT_PANE_TAB_ID"] == a["AGENT_PANE_TAB_ID"] || b["AGENT_PANE_SESSION_ID"] == a["AGENT_PANE_SESSION_ID"] {
		t.Fatalf("pane tabs share ids: %v %v", a, b)
	}

	// Reattaching does not restart the program, so its environment stands;
	// the ids are what the host would hand out again.
	if _, err := m.endpoint(first.ID); err != nil {
		t.Fatal(err)
	}
	if publicID("session", first.ID) != a["AGENT_PANE_SESSION_ID"] || publicID("tab", first.pane.id) != a["AGENT_PANE_TAB_ID"] {
		t.Fatal("ids are not stable for the session")
	}

	// The PTY is replaced in the same pane tab: same tab id, new session id.
	paneID := first.pane.id
	if err := m.Destroy(first.ID); err != nil {
		t.Fatal(err)
	}
	_, c := envSession(t, m, paneID, "")
	if c["AGENT_PANE_TAB_ID"] != a["AGENT_PANE_TAB_ID"] {
		t.Fatalf("replacement changed the tab id: %v vs %v", c, a)
	}
	if c["AGENT_PANE_SESSION_ID"] == a["AGENT_PANE_SESSION_ID"] {
		t.Fatal("replacement kept the session id")
	}
	if c["AGENT_PANE_AGENT"] != "<unset>" {
		t.Fatalf("replacement shell kept the old launch's agent %q", c["AGENT_PANE_AGENT"])
	}

	// A pane-tab id the host made before restarting keeps its identity.
	restarted := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, d := envSession(t, restarted, paneID, "")
	if d["AGENT_PANE_TAB_ID"] != a["AGENT_PANE_TAB_ID"] {
		t.Fatalf("restart changed the tab id: %v vs %v", d, a)
	}
}

func TestSessionEnvWithoutSkillOrPageChannel(t *testing.T) {
	old := skillPath
	skillPath = filepath.Join(t.TempDir(), "missing", "SKILL.md")
	t.Cleanup(func() { skillPath = old })
	env := sessionEnv(&Session{ID: "x", pane: newPaneTab()}, "", false)
	for _, k := range []string{pageFDEnv, envSkillPath, envAgent} {
		if _, ok := env[k]; ok {
			t.Errorf("%s set: %q", k, env[k])
		}
	}
	// A page command in such a terminal says where it is not.
	if _, err := read(env[pageFDEnv], "info"); err == nil || err.Error() != errNotInTerminal {
		t.Fatalf("read without a channel: %v", err)
	}
}

func TestPaneLockedOnlyAdoptsIdsItCouldHaveMade(t *testing.T) {
	m := NewSessionManager(0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.mu.Lock()
	defer m.mu.Unlock()
	made := newPaneTab().id
	if p := m.paneLocked(made); p.id != made {
		t.Fatalf("well-formed id not kept: %s", p.id)
	}
	for _, id := range []string{"pane-made-up", "../x", "tab-" + made[5:]} {
		if p := m.paneLocked(id); p.id == id {
			t.Fatalf("adopted %q", id)
		}
	}
}
