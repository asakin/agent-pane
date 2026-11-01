package main

import (
	"os"

	"agent-pane/internal/cmd"
)

var (
	version = "dev"
)

func main() {
	root := cmd.NewCmdRoot(version)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
