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
// Ancestor is a bare pid, so it must be a child of the caller that the caller
// has not yet waited for: an unreaped child keeps its pid, so the pid cannot
// name another process. Stop using the Policy before waiting for that child.
//
// Check binds the peer to one process before judging it, and a pid that is
// reused partway through the check makes it fail rather than judge another
// process. What is bound differs by platform:
//
//   - Linux: the process that connected, through the pidfd from SO_PEERPIDFD,
//     for as long as it lives. Whoever holds the socket now, that process is
//     judged, as the image it runs at check time, so a process that connects
//     and then execs an allowed executable passes. A connector that has
//     exited is refused. On kernels without SO_PEERPIDFD (before 6.5) the pid
//     is bound with pidfd_open after the fact, which leaves a short window for
//     reuse, and every policy with Ancestor is refused. Before Linux 5.3,
//     which has no pidfd_open, every peer is refused.
//   - macOS: the process that last used the socket, through the
//     LOCAL_PEERTOKEN audit token's pid version and the process's unique id.
//     If that process has since exec'd, the executable UUID the socket
//     recorded (LOCAL_PEERUUID) no longer matches its image and it is
//     refused. A child the socket was handed to is judged once it has used
//     the socket.
//
// Ancestry cannot see descendants that daemonise, such as a tmux server
// started from an Agent Pane terminal: its parent becomes init or launchd, so
// it is refused.
type Policy struct {
	Executables []string
	Ancestor    int
}

// ErrRejected is wrapped by every policy refusal.
var ErrRejected = errors.New("peer rejected")

// Get returns the uid and pid of the process on the other end of conn.
func Get(conn *net.UnixConn) (Peer, error) {
	peer, proc, err := get(conn)
	if err != nil {
		return Peer{}, err
	}
	proc.close()
	return peer, nil
}

func get(conn *net.UnixConn) (Peer, *process, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return Peer{}, nil, err
	}
	var peer Peer
	var proc *process
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		peer, proc, credErr = peerOf(int(fd))
	}); err != nil {
		return Peer{}, nil, err
	}
	return peer, proc, credErr
}

// Check gets the peer of conn and applies p to the process bound to it.
func Check(conn *net.UnixConn, p Policy) (Peer, error) {
	peer, proc, err := get(conn)
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrRejected, err)
	}
	defer proc.close()
	return peer, p.allow(peer, proc)
}

// Allow applies p to peer, looking its pid up now. Check is stronger: it
// judges the process that is on the socket, not whoever holds the pid.
func (p Policy) Allow(peer Peer) error {
	if peer.UID != os.Getuid() {
		return fmt.Errorf("%w: different user", ErrRejected)
	}
	proc, err := processOf(peer.PID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRejected, err)
	}
	defer proc.close()
	return p.allow(peer, proc)
}

func (p Policy) allow(peer Peer, proc *process) error {
	if peer.UID != os.Getuid() {
		return fmt.Errorf("%w: different user", ErrRejected)
	}
	if len(p.Executables) == 0 && p.Ancestor == 0 {
		return fmt.Errorf("%w: no peers allowed", ErrRejected)
	}
	if p.Ancestor > 0 {
		if err := proc.ancestryErr(); err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
	}
	if len(p.Executables) > 0 {
		if exe, err := proc.executable(); err == nil && matchesAny(exe, p.Executables) {
			return nil
		}
	}
	if p.Ancestor > 0 {
		if descends(proc, p.Ancestor) {
			return nil
		}
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

// descends reports whether proc is ancestor or one of its descendants. Each
// step's parent is checked to be the process that is still that child's
// parent, so a reused parent pid ends the walk, and the match itself must
// still be the bound process.
func descends(proc *process, ancestor int) bool {
	cur := proc
	defer func() {
		if cur != proc {
			cur.close()
		}
	}()
	for depth := 0; depth < 512; depth++ {
		pid := cur.pid()
		if pid == ancestor {
			return cur.alive() == nil
		}
		if pid <= 1 {
			return false
		}
		parent, err := cur.parent()
		if err != nil || parent.pid() == pid {
			if parent != nil {
				parent.close()
			}
			return false
		}
		if cur != proc {
			cur.close()
		}
		cur = parent
	}
	return false
}
