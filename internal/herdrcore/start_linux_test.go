//go:build linux

package herdrcore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSystemdRun puts a systemd-run on PATH that logs its arguments to the
// returned file and then either runs the command after "--" or, with fail
// set, refuses the way systemd-run does with no user manager.
func fakeSystemdRun(t *testing.T, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\n"
	if fail {
		body += "echo 'Failed to connect to bus: No medium found' >&2\nexit 1\n"
	} else {
		body += "while [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "systemd-run"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return log
}

func startOut(t *testing.T) string {
	t.Helper()
	out, err := runStart(func() *exec.Cmd {
		cmd := exec.Command("/bin/sh", "-c", "echo started in $HC_SESSION")
		cmd.Env = []string{"HC_SESSION=agent-pane"}
		return cmd
	})
	if err != nil {
		t.Fatalf("runStart: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestStartRunsInAUserScope(t *testing.T) {
	log := fakeSystemdRun(t, false)
	if got := startOut(t); got != "started in agent-pane" {
		t.Fatalf("output = %q", got)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal("systemd-run was not used:", err)
	}
	got := strings.Split(strings.TrimSpace(string(args)), "\n")
	if got[0] != "--user" || got[1] != "--scope" {
		t.Fatalf("systemd-run args = %q, want a user scope", got)
	}
	if tail := strings.Join(got[len(got)-4:], " "); tail != "-- /bin/sh -c echo started in $HC_SESSION" {
		t.Fatalf("systemd-run command = %q", tail)
	}
}

func TestStartFallsBackWithoutAUserManager(t *testing.T) {
	log := fakeSystemdRun(t, true)
	if got := startOut(t); got != "started in agent-pane" {
		t.Fatalf("output = %q", got)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("systemd-run was not tried:", err)
	}
}

func TestStartWithoutSystemdRun(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := startOut(t); got != "started in agent-pane" {
		t.Fatalf("output = %q", got)
	}
}
