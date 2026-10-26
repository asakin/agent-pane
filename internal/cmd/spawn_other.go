//go:build !darwin

package cmd

import (
	"fmt"

	"github.com/aymanbagabas/go-pty"
	"github.com/spf13/cobra"
)

func startPty(_ pty.Pty, cmd *pty.Cmd) (func() error, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Wait, nil
}

func NewCmdSpawn() *cobra.Command {
	return &cobra.Command{
		Use:    "spawn",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("spawn is only used on macOS")
		},
	}
}
