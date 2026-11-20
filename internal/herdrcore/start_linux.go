//go:build linux

package herdrcore

import "os/exec"

// runStart runs `hc server start`, which starts the core in the background
// and returns. newCmd makes the command; it is called more than once.
//
// A child of the native host sits in the browser's cgroup, where stopping
// the browser's scope would end the core and every terminal in it. Through
// `systemd-run --user --scope` the core gets a scope of its own under the
// user's systemd, the way a desktop starts a terminal. With no systemd-run,
// or no user manager to reach (a container, a confined browser), it starts
// as a child as before.
//
// systemd-run --scope exits with the status of the command it ran, so its
// failure cannot tell a scope it could not make from an hc that failed. A
// scope is tried first with `true`, and once that works hc runs in one
// with no second try, keeping hc's own error and output.
func runStart(newCmd func() *exec.Cmd) ([]byte, error) {
	if run, err := exec.LookPath("systemd-run"); err == nil && scoped(run, newCmd(), "true").Run() == nil {
		return scoped(run, newCmd()).CombinedOutput()
	}
	return newCmd().CombinedOutput()
}

// scoped makes cmd run argv, or by default its own command, through
// systemd-run in a new user scope.
func scoped(run string, cmd *exec.Cmd, argv ...string) *exec.Cmd {
	if len(argv) == 0 {
		argv = append([]string{cmd.Path}, cmd.Args[1:]...)
	}
	cmd.Path = run
	cmd.Args = append([]string{
		"systemd-run", "--user", "--scope", "--quiet", "--collect",
		"--description=Agent Pane terminal runtime",
		"--",
	}, argv...)
	return cmd
}
