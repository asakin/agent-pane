package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// Browser control comes from separate tools the person installs: Agent Pane
// finds them on the login PATH and shows their own setup steps, and installs
// nothing.
const (
	browserNote = "Agents can click, type and navigate in your Chrome with a browser-control tool. " +
		"Agent Pane does not control the browser; these tools do, with their own Chrome extensions and their own warnings. " +
		"Using one is your choice and your responsibility. " +
		"Agent Pane's page-read approvals and blocked domains apply to `agent-pane page` only: " +
		"an agent with one of these tools can act on any tab you connect to it."

	nodeDownloadURL          = "https://nodejs.org"
	playwriterInstall        = "npm i -g playwriter"
	playwriterExtensionURL   = "https://chromewebstore.google.com/detail/playwriter/jfeammnjpkecdekppnclgkkffahnhfhe"
	playwrightMCPReadmeURL   = "https://github.com/microsoft/playwright-mcp#readme"
	playwrightExtensionURL   = "https://chromewebstore.google.com/detail/playwright-extension/mmlmfjhmonkocbjadbfplnigmagldckm"
	playwrightMCPClaude      = "claude mcp add playwright -- npx @playwright/mcp@latest --extension"
	playwrightMCPCodex       = "codex mcp add playwright -- npx @playwright/mcp@latest --extension"
	playwrightMCPNodeMajor   = 20
	webStoreLabel            = "Open in Chrome Web Store"
	browserVersionProbeLimit = 5 * time.Second
	// A version line is short: probe output past this is dropped, so a tool
	// that keeps printing cannot grow the host's memory until the deadline.
	browserVersionOutputLimit = 4 << 10
)

// browserItem is one line of the report. Status is "found", "missing",
// "broken" (found, but it did not run) or "outdated" for a program Agent Pane
// looked for, and "action" for a step only the person can take, such as adding
// a Chrome extension.
type browserItem struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Optional bool     `json:"optional,omitempty"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail"`
	Commands []string `json:"commands,omitempty"`
	URL      string   `json:"url,omitempty"`
	URLLabel string   `json:"urlLabel,omitempty"`
}

// browserReport is what `agent-pane setup browser` prints and the
// browser.status RPC returns.
type browserReport struct {
	Note      string        `json:"note"`
	Items     []browserItem `json:"items"`
	NextSteps []string      `json:"nextSteps"`
}

// browserProgram is a program found on PATH and what its --version printed.
// Failed says why --version did not run to a clean exit, so a broken install
// is not shown as a working one.
type browserProgram struct {
	Found   bool
	Path    string
	Version string
	Failed  string
}

// runs reports whether p was found and its --version ran.
func (p browserProgram) runs() bool {
	return p.Found && p.Failed == ""
}

// browserStatus reports the browser-control tools on the PATH the launcher
// uses to find agents, read again so Check again sees a tool installed since
// the host started.
func browserStatus() browserReport {
	return browserReportFor(currentLoginPATH(), os.Getenv("PATHEXT"))
}

func browserReportFor(path, pathext string) browserReport {
	names := []string{"node", "npm", "playwriter", "npx"}
	found := make([]browserProgram, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = probeProgram(name, path, pathext)
		}()
	}
	wg.Wait()
	return buildBrowserReport(found[0], found[1], found[2], found[3])
}

// probeProgram finds name on path and runs it with --version.
func probeProgram(name, path, pathext string) browserProgram {
	env := []string{"PATH=" + path, "PATHEXT=" + pathext}
	bin, ok := lookPathIn(env, name)
	if !ok {
		return browserProgram{}
	}
	version, failed := programVersion(bin, path)
	return browserProgram{Found: true, Path: bin, Version: version, Failed: failed}
}

var versionRe = regexp.MustCompile(`v?\d+\.\d+[0-9A-Za-z.+-]*`)

// programVersion is the first version number bin --version prints, or "" when
// it prints none, and why it failed when it did not start, exit 0 or finish in
// time. A failure's output is not read for a version: a shim whose script is
// missing makes node print its own version in the error. npm's npm, npx and
// global commands are .cmd shims on Windows, run through cmd.exe as
// buildTerminalCommand runs a batch-file agent.
func programVersion(bin, path string) (version, failed string) {
	ctx, cancel := context.WithTimeout(context.Background(), browserVersionProbeLimit)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && isBatchFile(bin) {
		shell := windowsCmdExe()
		line, err := batchCommandLine(shell, []string{bin, "--version"})
		if err != nil {
			return "", err.Error()
		}
		cmd = exec.CommandContext(ctx, shell)
		setExecCmdLine(cmd, line)
	} else {
		cmd = exec.CommandContext(ctx, bin, "--version")
	}
	// A shim such as "#!/usr/bin/env node" finds node on the same PATH.
	cmd.Env = setEnvFold(os.Environ(), "PATH", path)
	cmd.Stdin = nil
	// A child left holding the output open does not hold the report up.
	cmd.WaitDelay = time.Second
	out := &headBuffer{limit: browserVersionOutputLimit}
	cmd.Stdout = out
	end, err := startProbe(cmd)
	if err != nil {
		return "", "it did not start: " + err.Error()
	}
	err = cmd.Wait()
	end()
	switch {
	case ctx.Err() != nil:
		return "", fmt.Sprintf("it did not finish within %s", browserVersionProbeLimit)
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Sprintf("it exited with code %d", exit.ExitCode())
		}
		return "", err.Error()
	}
	return versionRe.FindString(out.String()), ""
}

// headBuffer keeps the first limit bytes written to it and drops the rest. It
// reports every write as complete, so the program writing is not stopped by a
// broken pipe and still exits as it would. The buffer is a named field, not
// embedded: an embedded bytes.Buffer would give headBuffer its unbounded
// ReadFrom, which the io.Copy in os/exec uses instead of Write.
type headBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *headBuffer) String() string { return b.buf.String() }

// nodeMajor is the major version in a version such as "v22.3.0", or 0.
func nodeMajor(version string) int {
	major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

func buildBrowserReport(node, npm, playwriter, npx browserProgram) browserReport {
	var items []browserItem
	var steps []string

	nodeItem := browserItem{ID: "node", Name: "Node and npm"}
	switch {
	case node.Found && !node.runs(), npm.Found && !npm.runs():
		broken := node
		if node.runs() {
			broken = npm
		}
		nodeItem.Status = "broken"
		nodeItem.Detail = fmt.Sprintf("%s did not run (%s): %s", filepath.Base(broken.Path), broken.Path, broken.Failed)
		nodeItem.URL = nodeDownloadURL
		nodeItem.URLLabel = "Get Node.js"
	case node.Found && npm.Found:
		nodeItem.Status = "found"
		nodeItem.Detail = fmt.Sprintf("node %s, npm %s", versionOrUnknown(node.Version), versionOrUnknown(npm.Version))
	case node.Found:
		nodeItem.Status = "missing"
		nodeItem.Detail = fmt.Sprintf("node %s found, npm not found", versionOrUnknown(node.Version))
		nodeItem.URL = nodeDownloadURL
		nodeItem.URLLabel = "Get Node.js"
	default:
		nodeItem.Status = "missing"
		nodeItem.Detail = "Not found"
		nodeItem.URL = nodeDownloadURL
		nodeItem.URLLabel = "Get Node.js"
	}
	switch nodeItem.Status {
	case "broken":
		steps = append(steps, "Reinstall Node.js, which includes npm, from "+nodeDownloadURL+", then run `agent-pane setup browser` again.")
	case "missing":
		steps = append(steps, "Install Node.js, which includes npm, from "+nodeDownloadURL+", then run `agent-pane setup browser` again.")
	}
	items = append(items, nodeItem)

	cli := browserItem{ID: "playwriter-cli", Name: "Playwriter CLI"}
	switch {
	case playwriter.runs():
		cli.Status = "found"
		cli.Detail = "Installed, " + versionOrUnknown(playwriter.Version)
	case playwriter.Found:
		cli.Status = "broken"
		cli.Detail = fmt.Sprintf("Found at %s, but it did not run: %s. Reinstall it.", playwriter.Path, playwriter.Failed)
		cli.Commands = []string{playwriterInstall}
		if nodeItem.Status == "found" {
			steps = append(steps, "Reinstall the Playwriter CLI in a pane tab: `"+playwriterInstall+"`.")
		}
	default:
		cli.Status = "missing"
		cli.Detail = "Not installed"
		cli.Commands = []string{playwriterInstall}
		if nodeItem.Status == "found" {
			steps = append(steps, "Install the Playwriter CLI in a pane tab: `"+playwriterInstall+"`.")
		}
	}
	items = append(items, cli)

	items = append(items, browserItem{
		ID:       "playwriter-extension",
		Name:     "Playwriter extension",
		Status:   "action",
		Detail:   "Add it to Chrome, then click its icon on each tab an agent may control.",
		URL:      playwriterExtensionURL,
		URLLabel: webStoreLabel,
	})
	steps = append(steps, "Add the Playwriter extension from the Chrome Web Store ("+playwriterExtensionURL+"), then click its icon on the tab an agent may control.")

	mcp := browserItem{
		ID:       "playwright-mcp",
		Name:     "Playwright MCP",
		Optional: true,
		Status:   "action",
		Detail:   "For agents that support MCP. Add it with your agent's command; for other agents, see its README.",
		Commands: []string{playwrightMCPClaude, playwrightMCPCodex},
		URL:      playwrightMCPReadmeURL,
		URLLabel: "Playwright MCP README",
	}
	switch {
	case !node.Found || !npx.Found:
		mcp.Status = "missing"
		mcp.Detail = "Needs Node.js " + strconv.Itoa(playwrightMCPNodeMajor) + " or newer and npx, which were not found. " + mcp.Detail
	case !node.runs() || !npx.runs():
		mcp.Status = "broken"
		mcp.Detail = "Needs Node.js " + strconv.Itoa(playwrightMCPNodeMajor) + " or newer and npx, which were found but did not run. " + mcp.Detail
	case nodeMajor(node.Version) > 0 && nodeMajor(node.Version) < playwrightMCPNodeMajor:
		mcp.Status = "outdated"
		mcp.Detail = fmt.Sprintf("Needs Node.js %d or newer; found node %s. ", playwrightMCPNodeMajor, node.Version) + mcp.Detail
	}
	items = append(items, mcp)

	items = append(items, browserItem{
		ID:       "playwright-extension",
		Name:     "Playwright Extension",
		Optional: true,
		Status:   "action",
		Detail:   "Used by Playwright MCP's --extension mode. It asks which tabs a client may control.",
		URL:      playwrightExtensionURL,
		URLLabel: webStoreLabel,
	})
	steps = append(steps, "Optional, for agents that support MCP: add Playwright MCP with the command above and the Playwright Extension from the Chrome Web Store ("+playwrightExtensionURL+").")

	return browserReport{Note: browserNote, Items: items, NextSteps: steps}
}

func versionOrUnknown(version string) string {
	if version == "" {
		return "(version unknown)"
	}
	return version
}

// writeBrowserReport prints report as `agent-pane setup browser` shows it.
func writeBrowserReport(w io.Writer, report browserReport) error {
	var b strings.Builder
	b.WriteString("Browser control\n\n")
	b.WriteString(report.Note + "\n\n")
	for _, item := range report.Items {
		name := item.Name
		if item.Optional {
			name += " (optional)"
		}
		fmt.Fprintf(&b, "%s: %s\n", name, browserStatusLabel(item.Status))
		if item.Detail != "" {
			fmt.Fprintf(&b, "  %s\n", item.Detail)
		}
		for _, command := range item.Commands {
			fmt.Fprintf(&b, "  $ %s\n", command)
		}
		if item.URL != "" {
			fmt.Fprintf(&b, "  %s: %s\n", item.URLLabel, item.URL)
		}
	}
	if len(report.NextSteps) > 0 {
		b.WriteString("\nNext steps:\n")
		for i, step := range report.NextSteps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func browserStatusLabel(status string) string {
	switch status {
	case "found":
		return "found"
	case "missing":
		return "not found"
	case "broken":
		return "found, but it does not run"
	case "outdated":
		return "needs a newer Node.js"
	default:
		return "set up yourself"
	}
}

// NewCmdSetup is `agent-pane setup`, which reports what a feature needs.
func NewCmdSetup() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Show what an optional feature needs and how to set it up",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "browser",
		Short: "Report the browser-control tools agents can use, and how to set them up",
		Long: `Report the browser-control tools agents in pane tabs can use to click, type
and navigate in Chrome: Node and npm, the Playwriter CLI and extension, and
optionally Playwright MCP with the Playwright Extension.

Nothing is installed. The report shows each tool's own setup steps; adding a
Chrome extension is a step only the person can take.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeBrowserReport(cmd.OutOrStdout(), browserStatus())
		},
	})
	return cmd
}
