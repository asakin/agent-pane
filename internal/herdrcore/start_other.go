//go:build !windows && !linux

package herdrcore

import "os/exec"

// runStart runs `hc server start`, which starts the core in the background
// and returns. newCmd makes the command.
func runStart(newCmd func() *exec.Cmd) ([]byte, error) {
	return newCmd().CombinedOutput()
}
