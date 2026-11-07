//go:build unix

package jsonrpc

import (
	"io"
	"os"
	"syscall"
)

// nativeOutput returns stdout in non-blocking mode, so the runtime poller
// handles it and a write stalled on a full pipe can be interrupted.
func nativeOutput() io.Writer {
	fd, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return os.Stdout
	}
	syscall.CloseOnExec(fd)
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return os.Stdout
	}
	return os.NewFile(uintptr(fd), "stdout")
}
