//go:build windows

package herdrcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// binaryName is the bundled core's file name in libexec.
const binaryName = "hc.exe"

// socketPath is the core's HC_SOCKET on Windows, where herdr-core listens on
// a named pipe rather than a unix socket. Pipe names are one namespace for
// the whole machine, so the name carries the user and a hash of dataDir:
// two users, or two installs of one user, never share a core. It ends in
// .sock like herdr-core's own default (\\.\pipe\herdr-core-<user>-<session>.sock),
// because the core names its holder pipes by replacing that extension.
//
// herdr-core's source reads HC_SOCKET as the pipe name on Windows; that the
// pinned release accepts this override is still to be confirmed on Windows
// (RFC 0002, "What needs a prototype").
func socketPath(dataDir, _ string) string {
	user := strings.Map(func(r rune) rune {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, os.Getenv("USERNAME"))
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dataDir))))
	return `\\.\pipe\agent-pane-hc-` + user + "-" + hex.EncodeToString(sum[:8]) + ".sock"
}

// startFlags start `hc server start` with no console window and outside
// the caller's job object, when the job allows that. The browser runs its
// native host in a job, and a job set to kill its processes on close would
// otherwise take the core down with the browser. hc's own spawn of the
// core daemon also breaks away from the job; doing it here as well keeps
// the core out of the job when only our child may leave it.
//
// No CREATE_NEW_PROCESS_GROUP: a new group starts with Ctrl+C turned off,
// which the core and every program in its terminals would inherit.
//
// Unverified on a real browser: whether Chrome's and Edge's job for the
// native host allows breakaway (RFC 0002, "What needs a prototype").
const startFlags = windows.DETACHED_PROCESS

// runStart runs `hc server start`, which starts the core in the background
// and returns. newCmd makes the command; it is called again when the job
// refuses breakaway, since an exec.Cmd runs once.
func runStart(newCmd func() *exec.Cmd) ([]byte, error) {
	cmd := newCmd()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: startFlags | windows.CREATE_BREAKAWAY_FROM_JOB}
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.Process == nil && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// CreateProcess refuses breakaway from a job that does not allow
		// it. Start inside the job: the core then lives as long as it.
		cmd = newCmd()
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: startFlags}
		out, err = cmd.CombinedOutput()
	}
	return out, err
}

// dial connects to the core's named pipe.
func dial(ctx context.Context, pipe string) (net.Conn, error) {
	return dialPipe(ctx, pipe)
}
