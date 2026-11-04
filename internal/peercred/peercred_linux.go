//go:build linux

package peercred

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var errNoPidfd = errors.New("this kernel has no SO_PEERPIDFD (Linux 6.5 or later), so ancestry cannot be checked")

// process is one process, held by a pidfd so a reused pid is noticed. dir is
// its /proc/<pid> folder, opened while the pidfd showed the process alive, so
// reads through it stay on that process. Without a pidfd (older kernels) the
// process is only a pid, and ancestry is refused.
type process struct {
	num   int
	pidfd int
	dir   int
}

func peerOf(fd int) (Peer, *process, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Peer{}, nil, err
	}
	peer := Peer{UID: int(cred.Uid), PID: int(cred.Pid)}
	pidfd, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	if errors.Is(err, unix.ENOPROTOOPT) {
		return peer, &process{num: peer.PID, pidfd: -1, dir: -1}, nil
	}
	if err != nil {
		return Peer{}, nil, err
	}
	proc, err := bind(peer.PID, pidfd)
	if err != nil {
		return Peer{}, nil, err
	}
	return peer, proc, nil
}

func processOf(pid int) (*process, error) {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return nil, fmt.Errorf("no process %d: %w", pid, err)
	}
	return bind(pid, pidfd)
}

// bind opens /proc/<pid> and keeps it only if pidfd's process is still alive
// afterwards, which means pid had not been reused when the folder was opened.
// bind owns pidfd.
func bind(pid, pidfd int) (*process, error) {
	dir, err := unix.Open("/proc/"+strconv.Itoa(pid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Close(pidfd)
		return nil, fmt.Errorf("process %d is gone: %w", pid, err)
	}
	if err := unix.PidfdSendSignal(pidfd, 0, nil, 0); err != nil {
		unix.Close(dir)
		unix.Close(pidfd)
		return nil, fmt.Errorf("process %d is gone: %w", pid, err)
	}
	return &process{num: pid, pidfd: pidfd, dir: dir}, nil
}

func (p *process) close() {
	if p == nil {
		return
	}
	if p.dir >= 0 {
		unix.Close(p.dir)
	}
	if p.pidfd >= 0 {
		unix.Close(p.pidfd)
	}
}

func (p *process) pid() int { return p.num }

func (p *process) ancestryErr() error {
	if p.pidfd < 0 {
		return errNoPidfd
	}
	return nil
}

func (p *process) executable() (string, error) {
	if p.dir < 0 {
		return readlinkat(unix.AT_FDCWD, "/proc/"+strconv.Itoa(p.num)+"/exe")
	}
	return readlinkat(p.dir, "exe")
}

// parent binds p's parent. The parent's pidfd is opened between two reads of
// p's parent pid; when both reads agree, the pidfd is p's real parent, since a
// parent that exits before its child is replaced as the child's parent.
func (p *process) parent() (*process, error) {
	if p.dir < 0 {
		return nil, errNoPidfd
	}
	ppid, err := parentPid(p.dir)
	if err != nil {
		return nil, err
	}
	pidfd, err := unix.PidfdOpen(ppid, 0)
	if err != nil {
		return nil, fmt.Errorf("no process %d: %w", ppid, err)
	}
	if again, err := parentPid(p.dir); err != nil || again != ppid {
		unix.Close(pidfd)
		return nil, fmt.Errorf("parent of %d changed", p.num)
	}
	return bind(ppid, pidfd)
}

func readlinkat(dir int, path string) (string, error) {
	buf := make([]byte, unix.PathMax)
	n, err := unix.Readlinkat(dir, path, buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func parentPid(dir int) (int, error) {
	fd, err := unix.Openat(dir, "stat", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(fd)
	buf := make([]byte, 4096)
	n, err := unix.Read(fd, buf)
	if err != nil {
		return 0, err
	}
	raw := string(buf[:n])
	// The command name is in parentheses and may hold spaces; the fields
	// after the last ')' are state, then ppid.
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return 0, fmt.Errorf("malformed stat")
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed stat")
	}
	return strconv.Atoi(fields[1])
}
