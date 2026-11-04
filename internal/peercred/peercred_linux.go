//go:build linux

package peercred

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func peerOf(fd int) (Peer, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return Peer{}, err
	}
	return Peer{UID: int(cred.Uid), PID: int(cred.Pid)}, nil
}

func executable(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
}

func parentOf(pid int) (int, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	// The command name is in parentheses and may hold spaces; the fields
	// after the last ')' are state, then ppid.
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, fmt.Errorf("malformed stat for %d", pid)
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed stat for %d", pid)
	}
	return strconv.Atoi(fields[1])
}
