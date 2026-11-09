package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-pane/internal/herdrcore"
)

// fakeTeamCore is a core holding terminals by address, recording the calls
// the teamwork commands make.
type fakeTeamCore struct {
	terms  map[string]*herdrcore.Terminal
	next   int
	runs   []herdrcore.RunParams
	sends  []herdrcore.SendParams
	waits  []herdrcore.WaitParams
	reads  []herdrcore.ReadParams
	sendFn func(herdrcore.SendParams) error
	waitFn func(*herdrcore.Terminal) herdrcore.WaitResult
	runErr error
	closed bool
}

func newFakeTeamCore() *fakeTeamCore {
	return &fakeTeamCore{terms: map[string]*herdrcore.Terminal{
		"t1": {Address: "t1", State: "live", Labels: map[string]string{corePaneLabel: "tab-mine"}},
		"t2": {Address: "t2", State: "live", Labels: map[string]string{corePaneLabel: "tab-other"}},
	}, next: 3}
}

func (f *fakeTeamCore) Get(ctx context.Context, terminal string) (herdrcore.Terminal, error) {
	if t, ok := f.terms[terminal]; ok {
		return *t, nil
	}
	return herdrcore.Terminal{}, &herdrcore.Error{Kind: "not_found", Message: "no terminal"}
}

func (f *fakeTeamCore) List(ctx context.Context, labels map[string]string, all bool) ([]herdrcore.Terminal, error) {
	var out []herdrcore.Terminal
	for i := 1; i < f.next; i++ {
		t, ok := f.terms[fmt.Sprintf("t%d", i)]
		if !ok || (!all && t.State != "live") {
			continue
		}
		match := true
		for k, v := range labels {
			match = match && t.Labels[k] == v
		}
		if match {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (f *fakeTeamCore) Run(ctx context.Context, p herdrcore.RunParams) (herdrcore.Terminal, error) {
	f.runs = append(f.runs, p)
	if f.runErr != nil {
		return herdrcore.Terminal{}, f.runErr
	}
	t := &herdrcore.Terminal{Address: fmt.Sprintf("t%d", f.next), State: "live", Labels: p.Labels, Argv: p.Argv, CreatedMs: uint64(f.next)}
	f.terms[t.Address] = t
	f.next++
	return *t, nil
}

func (f *fakeTeamCore) Remove(ctx context.Context, terminal string) error {
	delete(f.terms, terminal)
	return nil
}

func (f *fakeTeamCore) Send(ctx context.Context, p herdrcore.SendParams) (herdrcore.SendResult, error) {
	f.sends = append(f.sends, p)
	if f.sendFn != nil {
		return herdrcore.SendResult{}, f.sendFn(p)
	}
	return herdrcore.SendResult{}, nil
}

func (f *fakeTeamCore) Wait(ctx context.Context, p herdrcore.WaitParams) (herdrcore.WaitResult, error) {
	f.waits = append(f.waits, p)
	return f.waitFn(f.terms[p.Terminal]), nil
}

func (f *fakeTeamCore) Read(ctx context.Context, p herdrcore.ReadParams) (herdrcore.ReadResult, error) {
	f.reads = append(f.reads, p)
	return herdrcore.ReadResult{Text: "screen of " + p.Terminal}, nil
}

func (f *fakeTeamCore) Close() error { f.closed = true; return nil }

// inPaneTab makes the test process look like a program in the Durable pane
// tab tab-mine, whose core terminal is t1, with core as Agent Pane's core.
func inPaneTab(t *testing.T, core *fakeTeamCore) {
	t.Helper()
	t.Setenv(envPresence, "1")
	t.Setenv(envTabID, "tab-mine")
	t.Setenv(envCoreTerminal, "t1")
	old := dialTeamCore
	dialTeamCore = func(context.Context) (teamCore, error) { return core, nil }
	t.Cleanup(func() { dialTeamCore = old })
	useTeamLock(t, t.TempDir())
}

// useTeamLock puts the team lock in dir for the test.
func useTeamLock(t *testing.T, dir string) {
	t.Helper()
	old := lockTeam
	lockTeam = func(ctx context.Context) (func(), error) { return lockFile(ctx, filepath.Join(dir, "team.lock")) }
	t.Cleanup(func() { lockTeam = old })
}

// agentPane runs the agent-pane command line args and returns its output.
func agentPane(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewCmdRoot("test")
	root.PersistentPreRunE = nil
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

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

func TestTeamLockExcludesConcurrentLaunches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "team.lock")
	ctx := context.Background()
	unlock, err := lockFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := lockFile(short, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while held: %v", err)
	}
	unlock()
	again, err := lockFile(ctx, path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
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

// Only the pane tab that started a helper reaches it.
func TestTeamHelpersBelongToTheirPaneTab(t *testing.T) {
	core := newFakeTeamCore()
	core.terms["t3"] = &herdrcore.Terminal{Address: "t3", State: "live", Labels: map[string]string{coreParentPaneLabel: "tab-other", coreHelperLabel: "theirs"}}
	core.next = 4
	inPaneTab(t, core)
	for _, args := range [][]string{{"send", "theirs", "hi"}, {"wait", "theirs"}, {"read", "theirs"}} {
		if _, err := agentPane(t, args...); err == nil || !strings.Contains(err.Error(), `no helper named "theirs"`) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if out, _ := agentPane(t, "ls"); strings.Contains(out, "theirs") {
		t.Fatalf("ls shows another pane tab's helper: %q", out)
	}
	if len(core.sends)+len(core.waits)+len(core.reads) != 0 {
		t.Fatal("a call reached the core")
	}
}

func TestTeamRefusesCallersOutsideADurablePaneTab(t *testing.T) {
	core := newFakeTeamCore()
	core.terms["t3"] = &herdrcore.Terminal{Address: "t3", State: "live", Labels: map[string]string{coreParentPaneLabel: "tab-mine", coreHelperLabel: "h"}}
	core.next = 4
	inPaneTab(t, core)

	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"outside Agent Pane", map[string]string{envPresence: ""}, errNotInTerminal},
		{"Built-in pane tab", map[string]string{envCoreTerminal: ""}, errNotDurable.Error()},
		{"another pane tab's terminal", map[string]string{envCoreTerminal: "t2"}, errNotDurable.Error()},
		{"a helper", map[string]string{envCoreTerminal: "t3"}, errNotDurable.Error()},
		{"an unknown terminal", map[string]string{envCoreTerminal: "t9"}, errNotDurable.Error()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			_, err := agentPane(t, "run", "--", "sh")
			if err == nil || err.Error() != c.want {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
	if len(core.runs) != 0 {
		t.Fatalf("started %d helpers", len(core.runs))
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

func TestHelperArgvUsesAgentDefinitions(t *testing.T) {
	lookup := func(id string) (Agent, bool) {
		if id == "codex" {
			return Agent{ID: "codex", Bin: "/usr/bin/codex", Args: []string{"--x"}}, true
		}
		return Agent{}, false
	}
	argv, agent := helperArgv([]string{"codex", "review this"}, lookup)
	if agent != "codex" || strings.Join(argv, "|") != "/usr/bin/codex|--x|review this" {
		t.Fatalf("%v %q", argv, agent)
	}
	argv, agent = helperArgv([]string{"go", "test"}, lookup)
	if agent != "" || strings.Join(argv, "|") != "go|test" {
		t.Fatalf("%v %q", argv, agent)
	}
	if got := defaultHelperName("", []string{"./My Script.sh"}); got != "my-script.sh" {
		t.Fatalf("name %q", got)
	}
}

// TestTeamRealCore runs the helper workflow against the pinned core when
// AGENT_PANE_TEST_HC names it: a program in a Durable pane tab's core
// terminal starts a helper, sends it work, waits for it and reads it.
func TestTeamRealCore(t *testing.T) {
	binary := os.Getenv("AGENT_PANE_TEST_HC")
	if binary == "" {
		t.Skip("set AGENT_PANE_TEST_HC to the pinned hc to run against a real core")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.MkdirTemp("", "apteam")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	hc := herdrcore.Private(data)
	hc.Binary = binary
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := hc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(hc.Binary, "server", "stop")
		cmd.Env = hc.Env(os.Environ())
		cmd.Run()
	})
	c, err := hc.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pane, err := c.Run(ctx, herdrcore.RunParams{Argv: []string{"sleep", "300"}, Labels: map[string]string{corePaneLabel: "tab-mine"}, NoParent: true})
	if err != nil {
		t.Fatal(err)
	}
	old := dialTeamCore
	dialTeamCore = func(ctx context.Context) (teamCore, error) { return hc.Dial(ctx) }
	t.Cleanup(func() { dialTeamCore = old })
	useTeamLock(t, data)
	t.Setenv(envPresence, "1")
	t.Setenv(envTabID, "tab-mine")
	t.Setenv(envCoreTerminal, pane.Address)

	script := `printf '\033]7501;state=idle\033\\'; read line; printf '\033]7501;state=working\033\\'; echo "helper got $line from $AGENT_PANE_TAB_ID"; printf '\033]7501;state=done\033\\'; sleep 300`
	if out, err := agentPane(t, "run", "--name", "echo", "--", "sh", "-c", script); err != nil || out != "echo\n" {
		t.Fatalf("run: %q %v", out, err)
	}
	eventually(t, "the helper to report idle", func() bool {
		out, _ := agentPane(t, "ls")
		return strings.Contains(out, "idle")
	})
	if _, err := agentPane(t, "send", "echo", "the task"); err != nil {
		t.Fatal(err)
	}
	out, err := agentPane(t, "wait", "--timeout", "20s", "echo")
	if err != nil || out != "echo: done\n" {
		t.Fatalf("wait: %q %v", out, err)
	}
	out, err = agentPane(t, "read", "echo")
	if err != nil || !strings.Contains(out, "helper got the task from tab-mine") {
		t.Fatalf("read: %q %v", out, err)
	}

	// The helper ends with the pane tab's program.
	if _, err := c.Kill(ctx, pane.Address); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the helper to end", func() bool {
		terms, err := c.List(ctx, map[string]string{coreParentPaneLabel: "tab-mine"}, false)
		return err == nil && len(terms) == 0
	})
}
