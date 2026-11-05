//go:build !darwin

package cmd

import (
	"fmt"

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
