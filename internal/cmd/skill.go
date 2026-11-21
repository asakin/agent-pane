package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// printSkill writes the installed Agent Pane skill to out.
func printSkill(out io.Writer) error {
	data, err := os.ReadFile(skillPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("the agent-pane skill is not installed at %s; run the Agent Pane installer again to put it back", skillPath)
	}
	if err != nil {
		return fmt.Errorf("failed to read the agent-pane skill: %w", err)
	}
	_, err = out.Write(data)
	return err
}
