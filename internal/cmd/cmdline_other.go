//go:build !windows

package cmd

import (
	"os/exec"

	"github.com/aymanbagabas/go-pty"
)

// setCmdLine is for Windows batch files; nothing sets one elsewhere.
func setCmdLine(*pty.Cmd, string) {}

// setExecCmdLine is for Windows batch files; nothing sets one elsewhere.
func setExecCmdLine(*exec.Cmd, string) {}
