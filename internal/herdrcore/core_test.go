package herdrcore

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
	// A program that prints ready, then echoes what is typed.
	argv := []string{"sh", "-c", "echo ready; exec cat"}
	if runtime.GOOS == "windows" {
		// Windows programs need SystemRoot and more; Env still makes the
		// core's HC_* and XDG paths private.
		base = os.Environ()
		argv = []string{"cmd.exe", "/d", "/q", "/k", "echo ready"}
	}

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
	for _, dir := range []string{core.StateDir, core.RunDir} {
		fi, err := os.Stat(dir)
		if err == nil && fi.IsDir() && runtime.GOOS == "windows" {
			continue // no unix modes: the directory inherits its ACL
		}
		if err != nil || fi.Mode().Perm() != 0o700 {
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
		Argv:   argv,
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

	testRealDoc(ctx, t, c)
}

// testRealDoc writes a document twice, loses a version race, and sees the
// core's doc event for each write.
func testRealDoc(ctx context.Context, t *testing.T, c *Client) {
	t.Helper()
	sub, err := c.Subscribe(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	d, err := c.DocGet(ctx, "agent-pane-test")
	if err != nil || d.Version != 0 || string(d.Value) != "null" {
		t.Fatalf("DocGet = %+v (%s), %v", d, d.Value, err)
	}
	v1, err := c.DocSet(ctx, "agent-pane-test", map[string]any{"spaces": []any{}}, 0)
	if err != nil || v1 == 0 {
		t.Fatalf("DocSet = %d, %v", v1, err)
	}
	if _, err := c.DocSet(ctx, "agent-pane-test", map[string]any{"stale": true}, 0); !IsKind(err, "conflict") {
		t.Fatalf("stale DocSet err = %v, want conflict", err)
	}
	d, err = c.DocGet(ctx, "agent-pane-test")
	if err != nil || d.Version != v1 || string(d.Value) != `{"spaces":[]}` {
		t.Fatalf("DocGet = %+v (%s), %v", d, d.Value, err)
	}
	for {
		e, err := sub.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if e.Type != "doc" {
			continue
		}
		t.Logf("doc event %+v data %s", e, e.Data)
		if name, ok := e.DocName(); !ok || name != "agent-pane-test" {
			t.Fatalf("doc event %+v (%s), want agent-pane-test", e, e.Data)
		}
		break
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
