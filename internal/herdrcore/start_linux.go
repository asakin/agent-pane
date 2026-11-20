//go:build linux

package herdrcore

import "os/exec"

// runStart runs `hc server start`, which starts the core in the background
// and returns. newCmd makes the command; it is called again when the user's
// systemd cannot run it.
//
// A child of the native host sits in the browser's cgroup, where stopping
// the browser's scope would end the core and every terminal in it. Through
// `systemd-run --user --scope` the core gets a scope of its own under the
// user's systemd, the way a desktop starts a terminal. With no systemd-run,
// or no user manager to reach (a container, a confined browser), it starts
// as a child as before.
func runStart(newCmd func() *exec.Cmd) ([]byte, error) {
	if cmd := scoped(newCmd()); cmd != nil {
		if out, err := cmd.CombinedOutput(); err == nil {
			return out, nil
		}
	}
	return newCmd().CombinedOutput()
}

// scoped returns cmd run by systemd-run in a new user scope, or nil when
// there is no systemd-run.
func scoped(cmd *exec.Cmd) *exec.Cmd {
	run, err := exec.LookPath("systemd-run")
	if err != nil {
		return nil
	}
	args := append([]string{
		"systemd-run", "--user", "--scope", "--quiet", "--collect",
		"--description=Agent Pane terminal runtime",
		"--", cmd.Path,
	}, cmd.Args[1:]...)
	cmd.Path = run
	cmd.Args = args
	return cmd
}
