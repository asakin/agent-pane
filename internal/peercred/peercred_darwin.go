//go:build darwin

package peercred

import (
	"bytes"
	"fmt"
	"unsafe"

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

// From <sys/proc_info.h>; libproc's proc_pidpath is this same call.
const (
	procInfoCallPidinfo   = 2
	procPidPathInfo       = 11
	procPidPathInfoMaxLen = 4 * 1024
)

// executable is proc_pidpath: the path of the executable the kernel mapped,
// resolved from its vnode. Unlike kern.procargs2 it is not the path the process
// was started with, which the process can rewrite and which may be relative.
func executable(pid int) (string, error) {
	buf := make([]byte, procPidPathInfoMaxLen)
	_, _, errno := unix.Syscall6(unix.SYS_PROC_INFO,
		procInfoCallPidinfo, uintptr(pid), procPidPathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return "", errno
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("no path for %d", pid)
	}
	return string(buf), nil
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
