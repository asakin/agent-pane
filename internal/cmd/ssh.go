package cmd

import (
	"fmt"
	"net/url"
	"os"

	"github.com/pomdtr/tweety/internal/jsonrpc"
	"github.com/spf13/cobra"
)

func NewCmdSSH() *cobra.Command {
	return &cobra.Command{
		Use:  "ssh <host>",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			options := map[string]string{
				"url": fmt.Sprintf("/term.html?mode=ssh&host=%s", url.QueryEscape(args[0])),
			}

			_, err := jsonrpc.SendRequest(os.Getenv("TWEETY_SOCKET"), "tabs.create", []any{options})
			if err != nil {
				return fmt.Errorf("failed to create tab: %w", err)
			}

			return nil
		},
	}
}
