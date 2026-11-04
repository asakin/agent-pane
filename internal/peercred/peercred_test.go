//go:build linux || darwin

package peercred

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func connPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "peercred")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		c, _ := ln.AcceptUnix()
		accepted <- c
	}()
	client, err := net.DialUnix("unix", nil, ln.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return server, client
}

func TestGetReturnsThisProcess(t *testing.T) {
	server, _ := connPair(t)
	peer, err := Get(server)
	if err != nil {
		t.Fatal(err)
	}
	if peer.UID != os.Getuid() || peer.PID != os.Getpid() {
		t.Fatalf("peer %+v, want uid %d pid %d", peer, os.Getuid(), os.Getpid())
	}
}

func TestCheckAcceptsOwnExecutable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	server, _ := connPair(t)
	if _, err := Check(server, Policy{Executables: []string{self}}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRejectsOtherExecutable(t *testing.T) {
	server, _ := connPair(t)
	_, err := Check(server, Policy{Executables: []string{"/bin/sh"}})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err %v, want ErrRejected", err)
	}
}

func TestAllowRejectsDifferentUID(t *testing.T) {
	peer := Peer{UID: os.Getuid() + 1, PID: os.Getpid()}
	if err := (Policy{}).Allow(peer); !errors.Is(err, ErrRejected) {
		t.Fatalf("err %v, want ErrRejected", err)
	}
	self, _ := os.Executable()
	if err := (Policy{Executables: []string{self}, Ancestor: os.Getpid()}).Allow(peer); !errors.Is(err, ErrRejected) {
		t.Fatalf("a matching process with another uid: err %v", err)
	}
}

func TestEmptyPolicyRejectsEveryone(t *testing.T) {
	self := Peer{UID: os.Getuid(), PID: os.Getpid()}
	if err := (Policy{}).Allow(self); !errors.Is(err, ErrRejected) {
		t.Fatalf("err %v, want ErrRejected", err)
	}
}

func TestAllowAncestry(t *testing.T) {
	self := Peer{UID: os.Getuid(), PID: os.Getpid()}
	if err := (Policy{Ancestor: os.Getpid()}).Allow(self); err != nil {
		t.Fatalf("self: %v", err)
	}
	if err := (Policy{Ancestor: os.Getppid()}).Allow(self); err != nil {
		t.Fatalf("parent: %v", err)
	}

	child := exec.Command("sleep", "5")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })

	childPeer := Peer{UID: os.Getuid(), PID: child.Process.Pid}
	if err := (Policy{Ancestor: os.Getpid()}).Allow(childPeer); err != nil {
		t.Fatalf("child of ancestor: %v", err)
	}
	if err := (Policy{Ancestor: child.Process.Pid}).Allow(self); !errors.Is(err, ErrRejected) {
		t.Fatalf("parent is not a descendant of its child: err %v", err)
	}
}

// startSleep runs sleep as a child and returns it, reaped at cleanup.
func startSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })
	return child
}

// A process bound before its pid is freed must not be judged by whoever gets
// that pid next.
func TestBoundProcessFailsAfterPidIsFreed(t *testing.T) {
	child := startSleep(t)
	proc, err := processOf(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	defer proc.close()
	if _, err := proc.executable(); err != nil {
		t.Fatalf("live child: %v", err)
	}
	_ = child.Process.Kill()
	_, _ = child.Process.Wait()
	if exe, err := proc.executable(); err == nil {
		t.Fatalf("executable of a freed pid: %q", exe)
	}
	if descends(proc, os.Getpid()) {
		t.Fatal("a freed pid still descends from its old parent")
	}
}

// The test binary re-runs itself as a child that connects, then execs sleep.
// Check judges the image the peer runs now, not the one that connected.
func TestCheckRefusesPeerThatExecedAfterConnect(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "peercred")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}

	child := exec.Command(self, "-test.run=^TestExecAfterConnectHelper$")
	child.Env = append(os.Environ(), "PEERCRED_SOCK="+sock, "PEERCRED_EXEC="+sleep)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })
	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The helper writes a byte once it has connected and is about to exec.
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	waitForExec(t, child.Process.Pid, self)

	if _, err := Check(conn, Policy{Executables: []string{self}}); !errors.Is(err, ErrRejected) {
		t.Fatalf("a peer that execed into sleep passed as the test binary: err %v", err)
	}
}

// waitForExec waits until pid no longer runs exe.
func waitForExec(t *testing.T, pid int, exe string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		proc, err := processOf(pid)
		if err != nil {
			t.Fatal(err)
		}
		got, err := proc.executable()
		proc.close()
		if err == nil && !matchesAny(got, []string{exe}) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("helper did not exec")
}

func TestExecAfterConnectHelper(t *testing.T) {
	sock := os.Getenv("PEERCRED_SOCK")
	if sock == "" {
		t.Skip()
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := conn.File()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the socket open across exec, as a hand-off would.
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	sleep := os.Getenv("PEERCRED_EXEC")
	t.Fatal(syscall.Exec(sleep, []string{sleep, "30"}, os.Environ()))
}
