//go:build unix && !darwin

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/aymanbagabas/go-pty"
)

// startPty starts cmd on the terminal and returns its wait and pid. pty.Cmd
// has no ExtraFiles, so the command is started the way go-pty does it, plus
// the inherited page channel, which the child sees as fd 3.
func startPty(tty pty.Pty, cmd *pty.Cmd, extra *os.File) (func() error, int, error) {
	unixTty, ok := tty.(pty.UnixPty)
	if !ok {
		return nil, 0, fmt.Errorf("not a unix terminal")
	}
	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	c := exec.Command(cmd.Path, argv[1:]...)
	c.Args = argv
	c.Env = cmd.Env
	c.Dir = cmd.Dir
	slave := unixTty.Slave()
	c.Stdin = slave
	c.Stdout = slave
	c.Stderr = slave
	if extra != nil {
		c.ExtraFiles = []*os.File{extra}
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := c.Start(); err != nil {
		return nil, 0, err
	}
	return c.Wait, c.Process.Pid, nil
}
