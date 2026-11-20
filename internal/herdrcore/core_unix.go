//go:build !windows

package herdrcore

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
)

// binaryName is the bundled core's file name in libexec.
const binaryName = "hc"

// socketPath is the core's unix socket, in its run directory.
func socketPath(_, runDir string) string {
	return filepath.Join(runDir, "core.sock")
}

// runStart runs `hc server start`, which starts the core in the background
// and returns. newCmd makes the command.
func runStart(newCmd func() *exec.Cmd) ([]byte, error) {
	return newCmd().CombinedOutput()
}

// dial connects to the core's unix socket.
func dial(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}
