//go:build !darwin

package cmd

import (
	"context"
	"fmt"
	"os/exec"

	"agent-pane/internal/herdrcore"

	"github.com/spf13/cobra"
)

func NewCmdSpawn() *cobra.Command {
	return &cobra.Command{
		Use:    "spawn",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("spawn is only used on macOS")
		},
	}
}

// launchCore starts the core the platform's default way (herdrcore.RunStart).
func launchCore(_ context.Context, newCmd func() *exec.Cmd) ([]byte, error) {
	return herdrcore.RunStart(newCmd)
}
