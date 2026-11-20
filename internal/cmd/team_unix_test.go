//go:build unix

package cmd

import (
	"strings"
	"testing"

	"agent-pane/internal/herdrcore"
)

func TestTeamHelperWorkflow(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	dir := t.TempDir()

	out, err := agentPane(t, "run", "--name", "review", "--cwd", dir, "--", "sh", "-c", "echo hi")
	if err != nil || out != "review\n" {
		t.Fatalf("run: %q %v", out, err)
	}
	run := core.runs[0]
	if run.Parent != "t1" || !run.NoNotify || run.Cwd != dir || strings.Join(run.Argv, " ") != "sh -c echo hi" {
		t.Fatalf("terminal.run %+v", run)
	}
	if run.Labels[coreParentPaneLabel] != "tab-mine" || run.Labels[coreHelperLabel] != "review" || run.Labels[corePaneLabel] != "" {
		t.Fatalf("labels %v", run.Labels)
	}
	// A helper is part of the pane tab but has no session of its own and no
	// way to the page.
	if run.Env[envTabID] != "tab-mine" || run.Env[envPresence] != "1" {
		t.Fatalf("env %v", run.Env)
	}
	for _, key := range []string{envSessionID, envSocket, pageFDEnv, envCoreTerminal} {
		if _, ok := run.Env[key]; ok {
			t.Fatalf("helper env has %s", key)
		}
	}

	if _, err := agentPane(t, "send", "review", "also", "check", "the", "tests"); err != nil {
		t.Fatal(err)
	}
	if s := core.sends[0]; s.Terminal != "t3" || s.Text != "also check the tests" || s.Caller != "t1" || s.Force {
		t.Fatalf("terminal.send %+v", s)
	}

	done := "done"
	core.waitFn = func(term *herdrcore.Terminal) herdrcore.WaitResult {
		term.Status = &herdrcore.Status{State: done, Msg: "no issues"}
		return herdrcore.WaitResult{Terminal: term.Address, Ended: true, Reason: "status", Status: &done}
	}
	out, err = agentPane(t, "wait", "--timeout", "5s", "review")
	if err != nil || out != "review: done: no issues\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
	if w := core.waits[0]; w.Terminal != "t3" || w.Caller != "t1" || w.Timeout != 5 {
		t.Fatalf("terminal.wait %+v", w)
	}

	out, err = agentPane(t, "read", "--lines", "60", "review")
	if err != nil || out != "screen of t3\n" {
		t.Fatalf("read: %q %v", out, err)
	}
	if r := core.reads[0]; r.Terminal != "t3" || r.Lines != 60 {
		t.Fatalf("terminal.read %+v", r)
	}

	out, err = agentPane(t, "ls")
	if err != nil || !strings.Contains(out, "review") || !strings.Contains(out, "done: no issues") || !strings.Contains(out, "sh -c echo hi") {
		t.Fatalf("ls: %q %v", out, err)
	}
	if !core.closed {
		t.Fatal("connection left open")
	}
}

func TestTeamWaitTimeoutSaysStillWorking(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	if _, err := agentPane(t, "run", "--name", "slow", "--", "sleep", "100"); err != nil {
		t.Fatal(err)
	}
	core.waitFn = func(term *herdrcore.Terminal) herdrcore.WaitResult {
		return herdrcore.WaitResult{Terminal: term.Address, Reason: "timeout"}
	}
	out, err := agentPane(t, "wait", "slow")
	if err != nil || out != "slow: still working\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
	if core.waits[0].Timeout != teamWaitTimeout.Seconds() {
		t.Fatalf("timeout %v", core.waits[0].Timeout)
	}
}

func TestTeamWaitOnFinishedHelperReportsExit(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	if _, err := agentPane(t, "run", "--", "make"); err != nil {
		t.Fatal(err)
	}
	code := 2
	core.terms["t3"].State, core.terms["t3"].ExitCode = "finished", &code
	out, err := agentPane(t, "wait", "make")
	if err != nil || out != "make: exited 2\n" || len(core.waits) != 0 {
		t.Fatalf("wait: %q %v (%d waits)", out, err, len(core.waits))
	}
	if _, err := agentPane(t, "send", "make", "x"); err == nil || !strings.Contains(err.Error(), "exited 2") {
		t.Fatalf("send to a finished helper: %v", err)
	}
	// Its name is free again; the finished one is removed.
	if out, err := agentPane(t, "run", "--name", "make", "--", "make"); err != nil || out != "make\n" {
		t.Fatalf("run again: %q %v", out, err)
	}
	if _, ok := core.terms["t3"]; ok {
		t.Fatal("finished helper kept")
	}
}

func TestTeamFailedReplacementKeepsFinishedHelper(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	if _, err := agentPane(t, "run", "--name", "tests", "--", "go", "test"); err != nil {
		t.Fatal(err)
	}
	code := 1
	core.terms["t3"].State, core.terms["t3"].ExitCode = "finished", &code
	core.runErr = &herdrcore.Error{Kind: "spawn_failed", Message: "no such directory"}
	if _, err := agentPane(t, "run", "--name", "tests", "--cwd", "/nonexistent", "--", "go", "test"); err == nil {
		t.Fatal("run with a failing start succeeded")
	}
	if _, ok := core.terms["t3"]; !ok {
		t.Fatal("failed replacement removed the finished helper")
	}
	out, err := agentPane(t, "read", "tests")
	if err != nil || out != "screen of t3\n" {
		t.Fatalf("read after a failed replacement: %q %v", out, err)
	}
}

func TestTeamSendRefusesBlockedHelperUnlessForced(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	if _, err := agentPane(t, "run", "--name", "h", "--", "sh"); err != nil {
		t.Fatal(err)
	}
	core.sendFn = func(p herdrcore.SendParams) error {
		if p.Force {
			return nil
		}
		return &herdrcore.Error{Kind: "blocked", Message: "t3 is blocked"}
	}
	if _, err := agentPane(t, "send", "h", "yes"); err == nil || !strings.Contains(err.Error(), "waiting for a person") {
		t.Fatalf("send: %v", err)
	}
	if _, err := agentPane(t, "send", "--force", "h", "yes"); err != nil {
		t.Fatal(err)
	}
}

func TestTeamRunNames(t *testing.T) {
	core := newFakeTeamCore()
	inPaneTab(t, core)
	for _, want := range []string{"sleep", "sleep-2"} {
		if out, err := agentPane(t, "run", "--", "/bin/sleep", "100"); err != nil || out != want+"\n" {
			t.Fatalf("run: %q %v, want %s", out, err, want)
		}
	}
	if _, err := agentPane(t, "run", "--name", "sleep", "--", "sleep", "1"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("duplicate name: %v", err)
	}
	if _, err := agentPane(t, "run", "--name", "Bad Name", "--", "sleep", "1"); err == nil || !strings.Contains(err.Error(), "invalid helper name") {
		t.Fatalf("bad name: %v", err)
	}
}
