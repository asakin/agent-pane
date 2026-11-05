//go:build linux

package peercred

import (
	"errors"
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

// Without SO_PEERPIDFD, every policy with Ancestor is refused, even when its
// executable would match.
func TestAncestryNeedsPidfd(t *testing.T) {
	self := Peer{UID: os.Getuid(), PID: os.Getpid()}
	proc, err := processOf(self.PID)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.close()
	proc.late = true
	if err := (Policy{Ancestor: os.Getpid()}).allow(self, proc); !errors.Is(err, ErrRejected) {
		t.Fatalf("ancestry without SO_PEERPIDFD: err %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := (Policy{Executables: []string{exe}, Ancestor: os.Getpid()}).allow(self, proc); !errors.Is(err, ErrRejected) {
		t.Fatalf("executable and ancestry without SO_PEERPIDFD: err %v", err)
	}
	if err := (Policy{Executables: []string{exe}}).allow(self, proc); err != nil {
		t.Fatalf("executable alone without SO_PEERPIDFD: %v", err)
	}
}

func TestCheckHoldsPidfd(t *testing.T) {
	server, _ := connPair(t)
	_, proc, err := get(server)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.close()
	if proc.late {
		t.Skip("kernel has no SO_PEERPIDFD")
	}
	if proc.pidfd < 0 || proc.dir < 0 {
		t.Fatalf("process %+v holds no pidfd or folder", *proc)
	}
	if _, err := proc.executable(); err != nil {
		t.Fatal(err)
	}
}
