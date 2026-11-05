//go:build windows

package cmd

import (
	"os"

	"github.com/aymanbagabas/go-pty"
)

// startPty starts cmd on the terminal. Page reading is not available here, so
// extra is always nil.
func startPty(_ pty.Pty, cmd *pty.Cmd, _ *os.File) (func() error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Wait, nil
}
