package herdrcore

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPrivatePaths(t *testing.T) {
	c := Private("/home/u/.local/share/agent-pane")
	want := Core{
		Binary:    "/home/u/.local/share/agent-pane/libexec/hc",
		StateDir:  "/home/u/.local/share/agent-pane/core/state",
		ConfigDir: "/home/u/.local/share/agent-pane/core/config",
		Socket:    "/home/u/.local/share/agent-pane/core/run/core.sock",
	}
	if c != want {
		t.Fatalf("Private = %+v, want %+v", c, want)
	}
}

func TestEnvReplacesTheUsersCore(t *testing.T) {
	c := Private("/data")
	got := c.Env([]string{
		"HOME=/home/u",
		"PATH=/usr/bin",
		// Inside a person's own hc terminal these name their core.
		"HC_SESSION=default",
		"HC_SOCKET=/run/user/1000/herdr-core/default.sock",
		"HC_TERMINAL_ID=t3",
		"HC_TERMINAL_SOCKET=/run/user/1000/herdr-core/default.holder.sock",
		"XDG_STATE_HOME=/home/u/.local/state",
		"XDG_CONFIG_HOME=/home/u/.config",
	})
	want := []string{
		"HOME=/home/u",
		"PATH=/usr/bin",
		"HC_SESSION=agent-pane",
		"HC_SOCKET=/data/core/run/core.sock",
		"XDG_STATE_HOME=/data/core/state",
		"XDG_CONFIG_HOME=/data/core/config",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Env =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestRealCore runs the pinned herdr-core when AGENT_PANE_TEST_HC names it,
// for example bin/herdr-core/linux_amd64/libexec/hc after
// scripts/dev-install.sh or scripts/fetch-herdr-core.sh. It runs the core in a
// scratch data dir, never a person's own session.
func TestRealCore(t *testing.T) {
	binary := os.Getenv("AGENT_PANE_TEST_HC")
	if binary == "" {
		t.Skip("set AGENT_PANE_TEST_HC to the pinned hc to run against a real core")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	// A short path: unix socket paths are limited to about 100 bytes.
	data, err := os.MkdirTemp("", "apcore")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	core := Private(data)
	core.Binary = binary
	home := filepath.Join(data, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	base := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := core.StartEnv(ctx, base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(core.Binary, "server", "stop")
		cmd.Env = core.Env(base)
		cmd.Run()
	})
	for _, dir := range []string{core.StateDir, filepath.Dir(core.Socket)} {
		if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v, want a 0700 directory", dir, fi.Mode(), err)
		}
	}

	c, err := core.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Hello.Generation != Generation {
		t.Fatalf("Hello = %+v", c.Hello)
	}

	labels := map[string]string{"agent-pane.pane": "test"}
	term, err := c.Run(ctx, RunParams{
		Argv:   []string{"sh", "-c", "echo ready; exec cat"},
		Cwd:    data,
		Labels: labels,
		Size:   &Size{Cols: 40, Rows: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if term.State != "live" || term.Labels["agent-pane.pane"] != "test" {
		t.Fatalf("Run = %+v", term)
	}
	list, err := c.List(ctx, labels, false)
	if err != nil || len(list) != 1 || list[0].Address != term.Address {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if _, err := c.Get(ctx, "no-such-terminal"); !IsKind(err, "not_found") {
		t.Fatalf("Get err = %v, want not_found", err)
	}

	a, err := c.Attach(ctx, AttachParams{Terminal: term.Address, Cols: 40, Rows: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	waitFor(t, a, "ready")
	if err := a.Resize(60, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Write([]byte("hello core\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a, "hello core")
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := c.Get(ctx, term.Address)
		if err != nil {
			t.Fatal(err)
		}
		if got.Cols == 60 && got.Rows == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("size = %dx%d after Resize(60, 20)", got.Cols, got.Rows)
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := c.Kill(ctx, term.Address); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, a); err != nil {
		t.Fatalf("stream after kill: %v", err)
	}
	if end, ok := a.End(); !ok || end.Reason != "killed" {
		t.Fatalf("End = %+v, %v", end, ok)
	}
	if err := c.Remove(ctx, term.Address); err != nil {
		t.Fatal(err)
	}
}

// waitFor reads a until its output contains want.
func waitFor(t *testing.T, a *Attachment, want string) {
	t.Helper()
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for !strings.Contains(out.String(), want) {
			n, err := a.Read(buf)
			if err != nil {
				done <- err
				return
			}
			out.Write(buf[:n])
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waiting for %q: %v; got %q", want, err, out.String())
		}
	case <-time.After(10 * time.Second):
		a.Close()
		<-done
		t.Fatalf("no %q in %q", want, out.String())
	}
}
