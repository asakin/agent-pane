// Package peercred checks who is on the other end of a unix socket.
package peercred

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Peer is the process that connected to a unix socket.
type Peer struct {
	UID int
	PID int
}

// Policy says which peers may connect. A peer must always have the caller's
// uid. When Executables is set, the peer's executable must be one of them.
// When Ancestor is set, the peer must be that pid or one of its descendants.
// When both are set, either one is enough. The zero Policy allows no one, so
// every socket has to say what it accepts.
//
// The peer is read once, when it connects. A process that connects and then
// execs, or hands the socket to a child, is judged as it was at connect time,
// and the ancestry walk follows parent pids, which can be reused.
type Policy struct {
	Executables []string
	Ancestor    int
}

// ErrRejected is wrapped by every policy refusal.
var ErrRejected = errors.New("peer rejected")

// Get returns the uid and pid of the process on the other end of conn.
func Get(conn *net.UnixConn) (Peer, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return Peer{}, err
	}
	var peer Peer
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		peer, credErr = peerOf(int(fd))
	}); err != nil {
		return Peer{}, err
	}
	return peer, credErr
}

// Check gets the peer of conn and applies p to it.
func Check(conn *net.UnixConn, p Policy) (Peer, error) {
	peer, err := Get(conn)
	if err != nil {
		return Peer{}, err
	}
	return peer, p.Allow(peer)
}

// Allow applies p to peer.
func (p Policy) Allow(peer Peer) error {
	if peer.UID != os.Getuid() {
		return fmt.Errorf("%w: different user", ErrRejected)
	}
	if len(p.Executables) == 0 && p.Ancestor == 0 {
		return fmt.Errorf("%w: no peers allowed", ErrRejected)
	}
	if len(p.Executables) > 0 {
		if exe, err := executable(peer.PID); err == nil && matchesAny(exe, p.Executables) {
			return nil
		}
	}
	if p.Ancestor > 0 && descends(peer.PID, p.Ancestor) {
		return nil
	}
	return fmt.Errorf("%w: process not allowed", ErrRejected)
}

func matchesAny(exe string, allowed []string) bool {
	exe = canonical(exe)
	for _, want := range allowed {
		if want != "" && canonical(want) == exe {
			return true
		}
	}
	return false
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// descends reports whether pid is ancestor or one of its descendants.
func descends(pid, ancestor int) bool {
	for depth := 0; pid > 1 && depth < 512; depth++ {
		if pid == ancestor {
			return true
		}
		parent, err := parentOf(pid)
		if err != nil || parent == pid {
			return false
		}
		pid = parent
	}
	return pid == ancestor
}
