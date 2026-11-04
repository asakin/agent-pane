//go:build linux || darwin

package peercred

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
