//go:build !windows

package cmd

import "github.com/aymanbagabas/go-pty"

// setCmdLine is for Windows batch files; nothing sets one elsewhere.
func setCmdLine(*pty.Cmd, string) {}
