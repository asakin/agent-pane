package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeVersionStub puts a program named name in dir that prints out when run
// with --version: a .cmd file on Windows, as npm's shims are there.
func writeVersionStub(t *testing.T, dir, name, out string) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\necho '" + out + "'\n"
	if runtime.GOOS == "windows" {
		path += ".cmd"
		body = "@echo off\r\necho " + out + "\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
}

// writeFailingStub puts a program named name in dir that prints out and exits
// 1, as a shim whose script is missing does.
func writeFailingStub(t *testing.T, dir, name, out string) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\necho '" + out + "'\nexit 1\n"
	if runtime.GOOS == "windows" {
		path += ".cmd"
		body = "@echo off\r\necho " + out + "\r\nexit /b 1\r\n"
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
}

func browserItemByID(t *testing.T, report browserReport, id string) browserItem {
	t.Helper()
	for _, item := range report.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("no %s item in %+v", id, report.Items)
	return browserItem{}
}

func TestBrowserReportFindsTheTools(t *testing.T) {
	bin := t.TempDir()
	writeVersionStub(t, bin, "node", "v22.3.0")
	writeVersionStub(t, bin, "npm", "10.8.1")
	writeVersionStub(t, bin, "npx", "10.8.1")
	writeVersionStub(t, bin, "playwriter", "playwriter/0.12.0 linux-x64 node-v22.3.0")

	report := browserReportFor(bin, ".COM;.EXE;.BAT;.CMD")

	if node := browserItemByID(t, report, "node"); node.Status != "found" || node.Detail != "node v22.3.0, npm 10.8.1" {
		t.Fatalf("node item %+v", node)
	}
	if cli := browserItemByID(t, report, "playwriter-cli"); cli.Status != "found" || !strings.Contains(cli.Detail, "0.12.0") || len(cli.Commands) != 0 {
		t.Fatalf("playwriter item %+v", cli)
	}
	if mcp := browserItemByID(t, report, "playwright-mcp"); mcp.Status != "action" || !mcp.Optional {
		t.Fatalf("playwright mcp item %+v", mcp)
	}
	for _, step := range report.NextSteps {
		if strings.Contains(step, playwriterInstall) || strings.Contains(step, nodeDownloadURL) {
			t.Fatalf("next steps install what is there: %q", step)
		}
	}
	if !strings.Contains(report.Note, "your choice and your responsibility") {
		t.Fatalf("note %q", report.Note)
	}
}

func TestBrowserReportWithNothingInstalled(t *testing.T) {
	report := browserReportFor(t.TempDir(), ".COM;.EXE;.BAT;.CMD")

	if node := browserItemByID(t, report, "node"); node.Status != "missing" || node.URL != nodeDownloadURL {
		t.Fatalf("node item %+v", node)
	}
	if cli := browserItemByID(t, report, "playwriter-cli"); cli.Status != "missing" || strings.Join(cli.Commands, "") != playwriterInstall {
		t.Fatalf("playwriter item %+v", cli)
	}
	if mcp := browserItemByID(t, report, "playwright-mcp"); mcp.Status != "missing" {
		t.Fatalf("playwright mcp item %+v", mcp)
	}
	if len(report.NextSteps) == 0 || !strings.Contains(report.NextSteps[0], nodeDownloadURL) {
		t.Fatalf("first step should be Node: %q", report.NextSteps)
	}
	if ext := browserItemByID(t, report, "playwriter-extension"); ext.URL != playwriterExtensionURL || ext.URLLabel != webStoreLabel {
		t.Fatalf("extension item %+v", ext)
	}
}

func TestBrowserReportSaysWhenNodeIsTooOldForPlaywrightMCP(t *testing.T) {
	bin := t.TempDir()
	writeVersionStub(t, bin, "node", "v18.19.0")
	writeVersionStub(t, bin, "npm", "10.2.3")
	writeVersionStub(t, bin, "npx", "10.2.3")

	report := browserReportFor(bin, ".COM;.EXE;.BAT;.CMD")

	mcp := browserItemByID(t, report, "playwright-mcp")
	if mcp.Status != "outdated" || !strings.Contains(mcp.Detail, "v18.19.0") || !strings.Contains(mcp.Detail, "20 or newer") {
		t.Fatalf("playwright mcp item %+v", mcp)
	}
	if cli := browserItemByID(t, report, "playwriter-cli"); cli.Status != "missing" {
		t.Fatalf("playwriter item %+v", cli)
	}
	if !strings.Contains(strings.Join(report.NextSteps, "\n"), playwriterInstall) {
		t.Fatalf("next steps leave out the Playwriter install: %q", report.NextSteps)
	}
}

func TestBrowserReportSaysWhenAToolDoesNotRun(t *testing.T) {
	bin := t.TempDir()
	writeVersionStub(t, bin, "node", "v22.3.0")
	writeVersionStub(t, bin, "npm", "10.8.1")
	writeVersionStub(t, bin, "npx", "10.8.1")
	writeFailingStub(t, bin, "playwriter", "Error: Cannot find module. Node.js v22.3.0")

	report := browserReportFor(bin, ".COM;.EXE;.BAT;.CMD")

	cli := browserItemByID(t, report, "playwriter-cli")
	if cli.Status != "broken" || !strings.Contains(cli.Detail, "exited with code 1") || strings.Join(cli.Commands, "") != playwriterInstall {
		t.Fatalf("playwriter item %+v", cli)
	}
	if !strings.Contains(strings.Join(report.NextSteps, "\n"), "Reinstall the Playwriter CLI") {
		t.Fatalf("next steps leave out the Playwriter reinstall: %q", report.NextSteps)
	}
}

func TestBrowserReportSaysWhenNodeDoesNotRun(t *testing.T) {
	bin := t.TempDir()
	writeFailingStub(t, bin, "node", "v22.3.0")
	writeVersionStub(t, bin, "npm", "10.8.1")
	writeVersionStub(t, bin, "npx", "10.8.1")

	report := browserReportFor(bin, ".COM;.EXE;.BAT;.CMD")

	if node := browserItemByID(t, report, "node"); node.Status != "broken" || node.URL != nodeDownloadURL {
		t.Fatalf("node item %+v", node)
	}
	if mcp := browserItemByID(t, report, "playwright-mcp"); mcp.Status != "broken" {
		t.Fatalf("playwright mcp item %+v", mcp)
	}
	if len(report.NextSteps) == 0 || !strings.HasPrefix(report.NextSteps[0], "Reinstall Node.js") {
		t.Fatalf("first step should be Node: %q", report.NextSteps)
	}
}

func TestNodeMajor(t *testing.T) {
	for in, want := range map[string]int{"v22.3.0": 22, "18.0.0": 18, "": 0, "vx": 0} {
		if got := nodeMajor(in); got != want {
			t.Errorf("nodeMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestHeadBufferKeepsOnlyTheFirstBytes(t *testing.T) {
	b := &headBuffer{limit: 8}
	for _, chunk := range []string{"v22.3", ".0 and", " more output"} {
		if n, err := b.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v, want %d, nil", chunk, n, err, len(chunk))
		}
	}
	if got := b.String(); got != "v22.3.0 " {
		t.Fatalf("kept %q, want %q", got, "v22.3.0 ")
	}
}

func TestSetupBrowserPrintsTheReport(t *testing.T) {
	var out bytes.Buffer
	if err := writeBrowserReport(&out, browserReportFor(t.TempDir(), "")); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Browser control", "Playwriter CLI: not found", "$ " + playwriterInstall, "Playwright MCP (optional)", playwrightMCPClaude, "Next steps:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
}
