package cmd

// Helpers for the Windows install layout. They run on every OS: the layout is
// built under a temporary folder with goos "windows", and the registry is a
// map.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fakeRegistry is userRegistry on a map.
type fakeRegistry struct {
	values     map[string]string
	path       string
	pathSet    bool
	broadcasts int
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{values: map[string]string{}} }

func (r *fakeRegistry) DefaultValue(key string) (string, bool, error) {
	v, ok := r.values[key]
	return v, ok, nil
}

func (r *fakeRegistry) SetDefaultValue(key, value string) error {
	r.values[key] = value
	return nil
}

func (r *fakeRegistry) UserPath() (string, error) { return r.path, nil }

func (r *fakeRegistry) SetUserPath(value string) error {
	r.path, r.pathSet = value, true
	return nil
}

func (r *fakeRegistry) EnvironmentChanged() { r.broadcasts++ }

const (
	winTestCore     = "hc.exe build"
	winTestConpty   = "conpty.dll build"
	winTestConsole  = "OpenConsole.exe build"
	winTestBinary   = "agent-pane.exe build"
	winTestVersion  = "0.3.0"
	winInstructions = "<!-- agent-pane:start -->\n# Agent Pane\n<!-- agent-pane:end -->\n"
)

func winTestLock() string {
	return "version " + testCoreVersionWin + "\ngeneration 1\n" +
		"linux_amd64 https://example.invalid/core.tar.gz 00 " + sha("other") + "\n" +
		"windows_amd64 https://example.invalid/core.zip 00 " + sha(winTestCore) + "\n" +
		"windows_amd64.file conpty/conpty.dll " + sha(winTestConpty) + "\n" +
		"windows_amd64.file conpty/x64/OpenConsole.exe " + sha(winTestConsole) + "\n"
}

const testCoreVersionWin = "0.1.19"

// writeWindowsRelease writes a Windows release archive's folder into dir.
func writeWindowsRelease(t *testing.T, dir string) {
	t.Helper()
	for rel, content := range map[string]string{
		"agent-pane.exe":                                     winTestBinary,
		"README.md":                                          "readme\n",
		"LICENSE":                                            "license\n",
		"instructions.md":                                    winInstructions,
		"agents/enabled.json":                                `{"agents":{}}`,
		"skills/agent-pane/SKILL.md":                         "name: agent-pane\n",
		"extensions/chrome/manifest.json":                    `{"version":"` + winTestVersion + `"}`,
		"extensions/chrome/background.js":                    "//\n",
		"herdr-core.lock":                                    winTestLock(),
		"libexec/hc.exe":                                     winTestCore,
		"libexec/conpty/conpty.dll":                          winTestConpty,
		"libexec/conpty/x64/OpenConsole.exe":                 winTestConsole,
		"licenses/herdr-core/LICENSE":                        "Apache License\n",
		"licenses/herdr-core/THIRD-PARTY-NOTICES/rust.txt":   "notices\n",
		"licenses/herdr-core/THIRD-PARTY-NOTICES/conpty.txt": "MIT\n",
	} {
		putFile(t, filepath.Join(dir, filepath.FromSlash(rel)), content)
	}
}

// windowsTestLayout is a Windows layout under a temporary folder, with
// Chrome's user data folder present.
func windowsTestLayout(t *testing.T) installLayout {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "Users", "me")
	l := layoutFor("windows", home, filepath.Join(home, "AppData", "Local"))
	if err := os.MkdirAll(browserDirs("windows", l.home, l.localAppData)[0].parent, 0o755); err != nil {
		t.Fatal(err)
	}
	return l
}

// testPlacer places src into l with a fake registry. Its doctor is
// windowsDoctor, which tests in the doctor change make real.
func testPlacer(t *testing.T, l installLayout, src string, reg userRegistry) (*placer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	p := &placer{
		l: l, src: src, reg: reg, out: &out, errOut: &errOut,
		getenv: func(name string) string {
			if name == "LOCALAPPDATA" {
				return l.localAppData
			}
			return ""
		},
		runInstalled: func(args ...string) (string, error) {
			if len(args) == 1 && args[0] == "--version" {
				return "agent-pane " + winTestVersion + "\n", nil
			}
			return windowsDoctor(l, reg)
		},
	}
	return p, &out, &errOut
}

// windowsTestEnv is a doctorEnv for a healthy Windows install of l whose
// registry is reg.
func windowsTestEnv(l installLayout, reg userRegistry) doctorEnv {
	return doctorEnv{
		goos: "windows", goarch: "amd64", version: winTestVersion,
		pathext:     ".COM;.EXE;.BAT;.CMD",
		coreVersion: func(string) (string, error) { return "hc " + testCoreVersionWin, nil },
		registry:    reg,
		getenv: func(name string) string {
			if name == "LOCALAPPDATA" {
				return l.localAppData
			}
			return ""
		},
	}
}

// windowsDoctor stands in for running the placed binary's doctor: it runs the
// checks and fails when one fails, as doctor does.
func windowsDoctor(l installLayout, reg userRegistry) (string, error) {
	var report bytes.Buffer
	results := runChecks(l, windowsTestEnv(l, reg))
	printChecks(&report, results)
	for _, r := range results {
		if r.status == checkFail {
			return report.String(), errors.New("doctor found problems")
		}
	}
	return report.String(), nil
}

// statuses gives each item its worst result.
func statuses(results []checkResult) map[string]checkStatus {
	rank := map[checkStatus]int{"": 0, checkOK: 1, checkWarn: 2, checkFail: 3}
	worst := map[string]checkStatus{}
	for _, r := range results {
		if rank[r.status] > rank[worst[r.item]] {
			worst[r.item] = r.status
		}
	}
	return worst
}
