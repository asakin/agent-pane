//go:build darwin

package peercred

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

func peerOf(fd int) (Peer, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return Peer{}, err
	}
	return Peer{UID: int(cred.Uid), PID: pid}, nil
}

// executable is proc_pidpath: the kern.procargs2 sysctl starts with argc and
// then the executable path the kernel launched.
func executable(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(raw) < 5 {
		return "", fmt.Errorf("no path for %d", pid)
	}
	path := raw[4:]
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	if len(path) == 0 {
		return "", fmt.Errorf("no path for %d", pid)
	}
	return string(path), nil
}

func parentOf(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if int(info.Proc.P_pid) != pid {
		return 0, fmt.Errorf("no process %d", pid)
	}
	return int(info.Eproc.Ppid), nil
}
