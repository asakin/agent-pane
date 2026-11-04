//go:build darwin

package peercred

import (
	"bytes"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// From <sys/proc_info.h> and <sys/proc_info_private.h>; libproc's
// proc_pidinfo and proc_pidpath are this same call.
const (
	procInfoCallPidinfo        = 2
	procPidPathInfo            = 11
	procPidPathInfoMaxLen      = 4 * 1024
	procPidtShortBSDInfo       = 13
	procPidUniqIdentifierInfo  = 17
	sizeofUniqIdentifierInfo   = 56
	sizeofBSDShortInfo         = 64
	auditTokenPid              = 5
	auditTokenEUID             = 1
	auditTokenPidVersion       = 7
	auditTokenWords            = 8
	uniqIdentifierUniqueID     = 16
	uniqIdentifierParentUnique = 24
	uniqIdentifierIDVersion    = 32
	bsdShortInfoPpid           = 4
)

var errChanged = errors.New("process changed during the check")

// process is one process image. The kernel gives every fork and every exec a
// new pid version, and every fork a new unique id, and never hands either out
// again. Each read checks the pid still has both before and after, so a
// reused pid, or an exec, makes the read fail.
type process struct {
	num      int
	version  int32
	uniqueID uint64
}

type uniqInfo struct {
	uniqueID       uint64
	parentUniqueID uint64
	version        int32
}

// peerOf reads the peer from LOCAL_PEERTOKEN, the audit token of the process
// that last used the socket, which names its pid version.
func peerOf(fd int) (Peer, *process, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return Peer{}, nil, err
	}
	var token [auditTokenWords]uint32
	size := uint32(unsafe.Sizeof(token))
	if _, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.SOL_LOCAL, unix.LOCAL_PEERTOKEN,
		uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&size)), 0); errno != 0 {
		return Peer{}, nil, errno
	}
	if size != uint32(unsafe.Sizeof(token)) {
		return Peer{}, nil, fmt.Errorf("short audit token")
	}
	if token[auditTokenEUID] != cred.Uid {
		return Peer{}, nil, errChanged
	}
	pid := int(token[auditTokenPid])
	info, err := uniqInfoOf(pid)
	if err != nil {
		return Peer{}, nil, err
	}
	proc := &process{num: pid, version: int32(token[auditTokenPidVersion]), uniqueID: info.uniqueID}
	if info.version != proc.version {
		return Peer{}, nil, errChanged
	}
	return Peer{UID: int(cred.Uid), PID: pid}, proc, nil
}

func processOf(pid int) (*process, error) {
	info, err := uniqInfoOf(pid)
	if err != nil {
		return nil, err
	}
	return &process{num: pid, version: info.version, uniqueID: info.uniqueID}, nil
}

func (p *process) close() {}

func (p *process) pid() int { return p.num }

func (p *process) ancestryErr() error { return nil }

// same reports an error unless p's pid still names p's image.
func (p *process) same() (uniqInfo, error) {
	info, err := uniqInfoOf(p.num)
	if err != nil {
		return uniqInfo{}, err
	}
	if info.version != p.version || info.uniqueID != p.uniqueID {
		return uniqInfo{}, errChanged
	}
	return info, nil
}

// executable is proc_pidpath: the path of the executable the kernel mapped,
// resolved from its vnode. Unlike kern.procargs2 it is not the path the process
// was started with, which the process can rewrite and which may be relative.
func (p *process) executable() (string, error) {
	if _, err := p.same(); err != nil {
		return "", err
	}
	buf := make([]byte, procPidPathInfoMaxLen)
	if err := procPidinfo(p.num, procPidPathInfo, buf); err != nil {
		return "", err
	}
	if _, err := p.same(); err != nil {
		return "", err
	}
	if i := bytes.IndexByte(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("no path for %d", p.num)
	}
	return string(buf), nil
}

// parent returns p's parent, which must be the process p was forked from:
// one the kernel reparented p to, or one that reused that pid, has another
// unique id.
func (p *process) parent() (*process, error) {
	info, err := p.same()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, sizeofBSDShortInfo)
	if err := procPidinfo(p.num, procPidtShortBSDInfo, buf); err != nil {
		return nil, err
	}
	if _, err := p.same(); err != nil {
		return nil, err
	}
	ppid := int(*(*uint32)(unsafe.Pointer(&buf[bsdShortInfoPpid])))
	parent, err := processOf(ppid)
	if err != nil {
		return nil, err
	}
	if parent.uniqueID != info.parentUniqueID {
		return nil, fmt.Errorf("parent of %d is not the process it was forked from", p.num)
	}
	return parent, nil
}

func uniqInfoOf(pid int) (uniqInfo, error) {
	buf := make([]byte, sizeofUniqIdentifierInfo)
	if err := procPidinfo(pid, procPidUniqIdentifierInfo, buf); err != nil {
		return uniqInfo{}, err
	}
	return uniqInfo{
		uniqueID:       *(*uint64)(unsafe.Pointer(&buf[uniqIdentifierUniqueID])),
		parentUniqueID: *(*uint64)(unsafe.Pointer(&buf[uniqIdentifierParentUnique])),
		version:        *(*int32)(unsafe.Pointer(&buf[uniqIdentifierIDVersion])),
	}, nil
}

func procPidinfo(pid, flavor int, buf []byte) error {
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO,
		procInfoCallPidinfo, uintptr(pid), uintptr(flavor), 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return errno
	}
	if flavor != procPidPathInfo && int(n) != len(buf) {
		return fmt.Errorf("short proc_info for %d", pid)
	}
	return nil
}
