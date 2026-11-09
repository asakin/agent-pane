package cmd

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// A program reports its status by printing a status report to its terminal
// (OSC 7501, the bundled core's status protocol). Durable (preview) pane tabs
// show it in the tab strip; elsewhere the terminal ignores it.
var (
	statusStates = []string{"idle", "working", "done", "blocked", "error"}
	statusKinds  = []string{"permission", "question", "auth"}
)

// maxStatusMsg bounds a status message in bytes; the core drops a report
// much longer than this.
const maxStatusMsg = 1024

// statusReport is the escape sequence reporting state with msg and kind;
// state "clear" clears the status.
func statusReport(state, msg, kind string) (string, error) {
	if state != "clear" && !slices.Contains(statusStates, state) {
		return "", fmt.Errorf("unknown status %q: use %s or clear", state, strings.Join(statusStates, ", "))
	}
	if kind != "" {
		if state != "blocked" {
			return "", errors.New("--kind is only for blocked")
		}
		if !slices.Contains(statusKinds, kind) {
			return "", fmt.Errorf("unknown kind %q: use %s", kind, strings.Join(statusKinds, ", "))
		}
	}
	if state == "clear" && msg != "" {
		return "", errors.New("clear takes no message")
	}
	report := "state=" + state
	if msg = trimStatusMsg(msg); msg != "" {
		report += ":msg=" + base64.StdEncoding.EncodeToString([]byte(msg))
	}
	if kind != "" {
		report += ":kind=" + kind
	}
	return "\x1b]7501;" + report + "\x1b\\", nil
}

// trimStatusMsg puts msg on one line and cuts it to maxStatusMsg bytes on a
// character boundary.
func trimStatusMsg(msg string) string {
	msg = strings.Join(strings.Fields(strings.ToValidUTF8(msg, "�")), " ")
	if len(msg) <= maxStatusMsg {
		return msg
	}
	cut := maxStatusMsg - len("…")
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}

func NewCmdStatus() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "status <idle|working|done|blocked|error|clear> [message...]",
		Short: "Report this program's status to its Agent Pane pane tab",
		Long: `Report what this program is doing, so its pane tab can show it.

  agent-pane status working "running tests"
  agent-pane status blocked --kind permission "allow writing to src/?"
  agent-pane status done
  agent-pane status clear

blocked means a person is needed; --kind says why (permission, question or
auth). The report goes to this process's terminal. Pane tabs on the Durable
(preview) terminal backend show it in the tab strip; Built-in pane tabs
ignore it.`,
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Getenv(envPresence) != "1" {
				return errors.New(errNotInTerminal)
			}
			report, err := statusReport(args[0], strings.Join(args[1:], " "), kind)
			if err != nil {
				return err
			}
			return writeStatusReport(report)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "why a blocked program needs a person: permission, question or auth")
	return cmd
}

// writeStatusReport writes report to the controlling terminal, so it
// reaches the pane tab even when a coding agent's command tool captures
// stdout.
func writeStatusReport(report string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("no terminal to report to: %w", err)
	}
	defer tty.Close()
	_, err = io.WriteString(tty, report)
	return err
}
