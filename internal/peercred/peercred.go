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
// Check binds the peer to one process before judging it, so a pid that is
// reused, or a process that execs, partway through the check makes the check
// fail rather than judge a different process: on Linux through the pidfd from
// SO_PEERPIDFD, on macOS through the pid version in the LOCAL_PEERTOKEN audit
// token. A peer that execs after it connects is judged as the image it runs
// at check time. On Linux kernels without SO_PEERPIDFD (before 6.5), Check
// refuses Ancestor policies. Ancestry cannot see descendants that daemonise,
// such as a tmux server started from an Agent Pane terminal: its parent
// becomes init or launchd, so it is refused.
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
		return Peer{}, err
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
	if len(p.Executables) > 0 {
		if exe, err := proc.executable(); err == nil && matchesAny(exe, p.Executables) {
			return nil
		}
	}
	if p.Ancestor > 0 {
		if err := proc.ancestryErr(); err != nil {
			return fmt.Errorf("%w: %v", ErrRejected, err)
		}
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
// parent, so a reused parent pid ends the walk.
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
			return true
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
