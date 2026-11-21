//go:build windows

package cmd

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/aymanbagabas/go-pty"
)

// startPty starts cmd on the terminal. Page reading is not available here, so
// extra is always nil.
func startPty(_ pty.Pty, cmd *pty.Cmd, _ *os.File) (func() error, int, error) {
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	return cmd.Wait, cmd.Process.Pid, nil
}

// setCmdLine makes cmd start with line as its whole command line, in place
// of one composed from its Args.
func setCmdLine(cmd *pty.Cmd, line string) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
}

// setExecCmdLine is setCmdLine for a program run without a terminal.
func setExecCmdLine(cmd *exec.Cmd, line string) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
}
