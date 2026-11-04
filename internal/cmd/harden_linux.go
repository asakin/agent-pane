//go:build linux

package cmd

import "golang.org/x/sys/unix"

// hardenHost marks the host non-dumpable, which makes /proc/<pid>/fd and
// ptrace owner-only for root. Other processes running as the user can no
// longer open its native-messaging stdin and stdout.
func hardenHost() error {
	return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
