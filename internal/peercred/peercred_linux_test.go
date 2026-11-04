//go:build linux

package peercred

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// bind must refuse a pidfd whose process is gone even when its pid now names
// another live process, as after pid reuse.
func TestBindRefusesReusedPid(t *testing.T) {
	child := startSleep(t)
	pidfd, err := unix.PidfdOpen(child.Process.Pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = child.Process.Kill()
	_, _ = child.Process.Wait()
	// Pretend the pid was reused by this process: /proc/<self> exists.
	if proc, err := bind(os.Getpid(), pidfd); err == nil {
		proc.close()
		t.Fatal("bound a pid whose pidfd process is gone")
	}
}

func TestAncestryNeedsPidfd(t *testing.T) {
	self := Peer{UID: os.Getuid(), PID: os.Getpid()}
	proc := &process{num: self.PID, pidfd: -1, dir: -1}
	err := (Policy{Ancestor: os.Getpid()}).allow(self, proc)
	if err == nil {
		t.Fatal("ancestry passed without a pidfd")
	}
}

func TestCheckHoldsPidfd(t *testing.T) {
	server, _ := connPair(t)
	_, proc, err := get(server)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.close()
	if proc.pidfd < 0 {
		t.Skip("kernel has no SO_PEERPIDFD")
	}
}
