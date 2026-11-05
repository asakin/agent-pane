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
// reads through it stay on that process. The pidfd and the folder both
// survive exec, so reads see the image the process runs now. late is set when
// the kernel has no SO_PEERPIDFD and the pidfd was opened from the peer's pid
// after the fact, which a quick enough pid reuse can defeat; ancestry is
// refused then.
type process struct {
	num   int
	pidfd int
	dir   int
	late  bool
}

func peerOf(fd int) (Peer, *process, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Peer{}, nil, err
	}
	peer := Peer{UID: int(cred.Uid), PID: int(cred.Pid)}
	pidfd, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	if errors.Is(err, unix.ENOPROTOOPT) {
		proc, err := processOf(peer.PID)
		if err != nil {
			return Peer{}, nil, err
		}
		proc.late = true
		return peer, proc, nil
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
	proc := &process{num: pid, pidfd: pidfd, dir: dir}
	if err := proc.alive(); err != nil {
		proc.close()
		return nil, err
	}
	return proc, nil
}

func (p *process) close() {
	if p == nil {
		return
	}
	unix.Close(p.dir)
	unix.Close(p.pidfd)
}

func (p *process) pid() int { return p.num }

func (p *process) ancestryErr() error {
	if p.late {
		return errNoPidfd
	}
	return nil
}

// alive reports an error unless p's process is still running. A zombie has
// exited, so it is gone too, even though its pid is not free yet.
func (p *process) alive() error {
	if err := unix.PidfdSendSignal(p.pidfd, 0, nil, 0); err != nil {
		return fmt.Errorf("process %d is gone: %w", p.num, err)
	}
	if state, _, err := stat(p.dir); err != nil || state == "Z" {
		return fmt.Errorf("process %d is gone", p.num)
	}
	return nil
}

func (p *process) executable() (string, error) {
	return readlinkat(p.dir, "exe")
}

// parent binds p's parent. The parent's pidfd is opened between two reads of
// p's parent pid; when both reads agree, the pidfd is p's real parent, since a
// parent that exits before its child is replaced as the child's parent.
func (p *process) parent() (*process, error) {
	_, ppid, err := stat(p.dir)
	if err != nil {
		return nil, err
	}
	pidfd, err := unix.PidfdOpen(ppid, 0)
	if err != nil {
		return nil, fmt.Errorf("no process %d: %w", ppid, err)
	}
	if _, again, err := stat(p.dir); err != nil || again != ppid {
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

// stat returns the state and parent pid from the stat file in dir.
func stat(dir int) (string, int, error) {
	fd, err := unix.Openat(dir, "stat", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", 0, err
	}
	defer unix.Close(fd)
	buf := make([]byte, 4096)
	n, err := unix.Read(fd, buf)
	if err != nil {
		return "", 0, err
	}
	raw := string(buf[:n])
	// The command name is in parentheses and may hold spaces; the fields
	// after the last ')' are state, then ppid.
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return "", 0, fmt.Errorf("malformed stat")
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 2 {
		return "", 0, fmt.Errorf("malformed stat")
	}
	ppid, err := strconv.Atoi(fields[1])
	return fields[0], ppid, err
}
