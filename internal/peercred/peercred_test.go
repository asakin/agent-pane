//go:build linux || darwin

package peercred

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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
	if descends(proc, child.Process.Pid) {
		t.Fatal("a freed pid matched itself as the ancestor")
	}
}

// listen returns a listener on a fresh socket path.
func listen(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
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
	t.Cleanup(func() { _ = ln.Close() })
	return ln, sock
}

// startHelper re-runs the test binary as TestPeerHelper in the given mode.
func startHelper(t *testing.T, sock, mode string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestPeerHelper$")
	child.Env = append(os.Environ(), "PEERCRED_SOCK="+sock, "PEERCRED_MODE="+mode, "PEERCRED_EXEC="+sleep)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _, _ = child.Process.Wait() })
	return child
}

// execedPeer has a helper connect, write a byte and then exec sleep, and
// returns the accepted connection once the exec has happened.
func execedPeer(t *testing.T) *net.UnixConn {
	t.Helper()
	ln, sock := listen(t)
	child := startHelper(t, sock, "exec")
	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	waitForExec(t, child.Process.Pid, self)
	return conn
}

// An allowed binary that connects and then execs something else is refused.
func TestCheckRefusesPeerThatExecedAfterConnect(t *testing.T) {
	conn := execedPeer(t)
	self, _ := os.Executable()
	if _, err := Check(conn, Policy{Executables: []string{self}}); !errors.Is(err, ErrRejected) {
		t.Fatalf("a peer that execed into sleep passed as the test binary: err %v", err)
	}
}

// A disallowed binary (the test binary) connects, writes, then execs into the
// allowed one (sleep). macOS refuses it, because the socket's LOCAL_PEERUUID
// still names the old image. Linux has no record of the image that used the
// socket, so it judges the current image and lets it pass, as documented.
func TestCheckPeerThatExecedIntoAllowedBinary(t *testing.T) {
	conn := execedPeer(t)
	sleep, _ := exec.LookPath("sleep")
	_, err := Check(conn, Policy{Executables: []string{sleep}})
	switch runtime.GOOS {
	case "darwin":
		if !errors.Is(err, ErrRejected) {
			t.Fatalf("a peer that execed into the allowed binary passed: err %v", err)
		}
	case "linux":
		if err != nil {
			t.Fatalf("Linux judges the current image, so this passes; got %v", err)
		}
	}
}

// A connector that hands its socket to a child and exits is refused, however
// broad the policy: on Linux the bound connector is gone, on macOS the last
// user of the socket is.
func TestCheckRefusesSocketHandedToChildAfterConnectorExits(t *testing.T) {
	ln, sock := listen(t)
	child := startHelper(t, sock, "handoff")
	conn, err := ln.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("helper: %v", err)
	}

	self, _ := os.Executable()
	sleep, _ := exec.LookPath("sleep")
	policy := Policy{Executables: []string{self, sleep}, Ancestor: 1}
	if _, err := Check(conn, policy); !errors.Is(err, ErrRejected) {
		t.Fatalf("a socket whose connector exited passed: err %v", err)
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

// TestPeerHelper runs only as a child of the tests above. In "exec" mode it
// connects, writes a byte and execs sleep, keeping the socket open. In
// "handoff" mode it connects, starts sleep holding the socket, writes sleep's
// pid and exits.
func TestPeerHelper(t *testing.T) {
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
	sleep := os.Getenv("PEERCRED_EXEC")
	switch os.Getenv("PEERCRED_MODE") {
	case "exec":
		// Keep the socket open across exec, as a hand-off would.
		if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		t.Fatal(syscall.Exec(sleep, []string{sleep, "30"}, os.Environ()))
	case "handoff":
		heir := exec.Command(sleep, "30")
		heir.ExtraFiles = []*os.File{f}
		if err := heir.Start(); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(f, "%d\n", heir.Process.Pid); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
}
