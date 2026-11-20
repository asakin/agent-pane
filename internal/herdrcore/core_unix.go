//go:build !windows

package herdrcore

import (
	"context"
	"net"
	"path/filepath"
)

// binaryName is the bundled core's file name in libexec.
const binaryName = "hc"

// socketPath is the core's unix socket, in its run directory.
func socketPath(_, runDir string) string {
	return filepath.Join(runDir, "core.sock")
}

// dial connects to the core's unix socket.
func dial(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}
