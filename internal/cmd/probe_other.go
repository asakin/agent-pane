//go:build !windows

package cmd

import "os/exec"

// startProbe starts cmd. The Windows one bounds what cmd starts, too.
func startProbe(cmd *exec.Cmd) (end func(), err error) {
	return func() {}, cmd.Start()
}
