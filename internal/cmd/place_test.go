package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// placeOnce places src and fails the test on an error.
func placeOnce(t *testing.T, l installLayout, src string, reg userRegistry) (string, string) {
	t.Helper()
	p, out, errOut := testPlacer(t, l, src, reg)
	if err := p.run(); err != nil {
		t.Fatalf("place: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	return out.String(), errOut.String()
}

func TestPlaceWindowsInstallsAndIsIdempotent(t *testing.T) {
	l := windowsTestLayout(t)
	src := filepath.Join(t.TempDir(), "agent-pane-0.3.0-windows_amd64")
	writeWindowsRelease(t, src)
	reg := newFakeRegistry()
	reg.path = `C:\Windows\System32;%USERPROFILE%\bin`

	out, errOut := placeOnce(t, l, src, reg)
	if errOut != "" {
		t.Errorf("stderr: %s", errOut)
	}
	for _, want := range []string{
		`agent-pane: updated %LOCALAPPDATA%\` + filepath.Join("agent-pane", "release"),
		`agent-pane: updated %LOCALAPPDATA%\` + filepath.Join("agent-pane", "bin", "agent-pane.exe"),
		`agent-pane: updated HKCU\Software\Google\Chrome\NativeMessagingHosts\com.agentpane.native`,
		"agent-pane: updated the user Path",
		`agent-pane: updated %USERPROFILE%\AGENTS.md`,
		"agent-pane: installed agent-pane " + winTestVersion,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	for rel, want := range map[string]string{
		"bin/agent-pane.exe":                                 winTestBinary,
		"libexec/hc.exe":                                     winTestCore,
		"libexec/conpty/conpty.dll":                          winTestConpty,
		"libexec/conpty/x64/OpenConsole.exe":                 winTestConsole,
		"release/agent-pane.exe":                             winTestBinary,
		"release/README.md":                                  "readme\n",
		"agents/enabled.json":                                `{"agents":{}}`,
		"skills/agent-pane/SKILL.md":                         "name: agent-pane\n",
		"extensions/chrome/background.js":                    "//\n",
		"licenses/herdr-core/THIRD-PARTY-NOTICES/conpty.txt": "MIT\n",
	} {
		if got := readString(t, filepath.Join(l.dataDir, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if got := readString(t, filepath.Join(l.configDir, "AGENTS.md")); got != winInstructions {
		t.Errorf("config AGENTS.md = %q", got)
	}
	if got := readString(t, filepath.Join(l.home, "AGENTS.md")); got != winInstructions {
		t.Errorf("~/AGENTS.md = %q", got)
	}
	if got, want := readString(t, filepath.Join(l.home, "CLAUDE.md")), managedStart+"\n@AGENTS.md\n"+managedEnd+"\n"; got != want {
		t.Errorf("~/CLAUDE.md = %q, want %q", got, want)
	}
	for _, dir := range []string{".agents", ".claude"} {
		copyDir := filepath.Join(l.home, dir, "skills", "agent-pane")
		if readString(t, filepath.Join(copyDir, "SKILL.md")) != "name: agent-pane\n" || readString(t, filepath.Join(copyDir, skillMarker)) != skillMarkerText {
			t.Errorf("%s is not a marked copy of the skill", copyDir)
		}
	}

	var m struct {
		Name           string   `json:"name"`
		Description    string   `json:"description"`
		Path           string   `json:"path"`
		Type           string   `json:"type"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal([]byte(readString(t, l.manifest)), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != nativeHostName || m.Path != l.binary || m.Type != "stdio" || len(m.AllowedOrigins) != 1 || m.AllowedOrigins[0] != extensionOrigin || m.Description == "" {
		t.Errorf("manifest = %+v", m)
	}
	if problem := checkManifest(l.manifest, l.binary); problem != "" {
		t.Error(problem)
	}

	browsers := browserDirs("windows", l.home, l.localAppData)
	if got := reg.values[browsers[0].key]; got != l.manifest {
		t.Errorf("Chrome's key = %q, want %q", got, l.manifest)
	}
	if _, ok := reg.values[browsers[1].key]; ok {
		t.Errorf("Edge's key was written with no Edge user data folder")
	}
	if want := `C:\Windows\System32;%USERPROFILE%\bin;` + filepath.Dir(l.binary); reg.path != want {
		t.Errorf("user Path = %q, want %q", reg.path, want)
	}
	if reg.broadcasts != 1 {
		t.Errorf("%d WM_SETTINGCHANGE broadcasts, want 1", reg.broadcasts)
	}

	// A second run, from the same folder or from the kept release as doctor
	// --fix does, prints nothing.
	for _, from := range []string{src, l.release} {
		reg.pathSet = false
		out, errOut := placeOnce(t, l, from, reg)
		if out != "" || errOut != "" {
			t.Errorf("second run from %s printed:\n%s%s", from, out, errOut)
		}
		if reg.pathSet {
			t.Error("second run wrote the user Path")
		}
	}
}

func TestPlaceWindowsUpdate(t *testing.T) {
	l := windowsTestLayout(t)
	src := t.TempDir()
	writeWindowsRelease(t, src)
	reg := newFakeRegistry()
	placeOnce(t, l, src, reg)

	// A copy a previous run could not delete, and a stale core file.
	putFile(t, l.binary+".old-1", "older binary")
	putFile(t, filepath.Join(l.dataDir, "libexec", "conpty", "stale.dll"), "stale")
	putFile(t, filepath.Join(src, "agent-pane.exe"), "agent-pane.exe 0.4.0")
	putFile(t, filepath.Join(src, "libexec", "conpty", "conpty.dll"), "conpty.dll 0.4.0")
	putFile(t, filepath.Join(src, "herdr-core.lock"), strings.Replace(winTestLock(), sha(winTestConpty), sha("conpty.dll 0.4.0"), 1))

	out, _ := placeOnce(t, l, src, reg)
	for _, want := range []string{
		filepath.Join("bin", "agent-pane.exe") + "\n",
		filepath.Join("libexec", "conpty", "conpty.dll") + "\n",
		filepath.Join("libexec", "conpty", "stale.dll") + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hc.exe") {
		t.Errorf("unchanged hc.exe reported:\n%s", out)
	}
	if got := readString(t, l.binary); got != "agent-pane.exe 0.4.0" {
		t.Errorf("binary = %q", got)
	}
	if got := readString(t, filepath.Join(l.release, "agent-pane.exe")); got != "agent-pane.exe 0.4.0" {
		t.Errorf("release binary = %q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(l.binary))
	if len(entries) != 1 {
		t.Errorf("bin holds %d files, want only agent-pane.exe: %v", len(entries), entries)
	}
	if exists(filepath.Join(l.dataDir, "libexec", "conpty", "stale.dll")) {
		t.Error("stale core file kept")
	}
}

func TestPlaceFileMovesTheOldCopyAside(t *testing.T) {
	dir := t.TempDir()
	src, dest := filepath.Join(dir, "new"), filepath.Join(dir, "bin", "x.exe")
	putFile(t, src, "new")
	putFile(t, dest, "old")
	putFile(t, dest+".old-1", "older, still running")
	if changed, err := placeFile(src, dest); err != nil || !changed {
		t.Fatalf("placeFile = %v, %v", changed, err)
	}
	if got := readString(t, dest); got != "new" {
		t.Errorf("dest = %q", got)
	}
	// .old-1 was taken, so the replaced copy went to .old-2 and was deleted.
	if exists(dest+".old-2") || !exists(dest+".old-1") {
		t.Error("the replaced copy was not moved to a free name and deleted")
	}
	removeOldCopies(filepath.Dir(dest))
	if exists(dest + ".old-1") {
		t.Error("removeOldCopies kept an old copy")
	}
	if changed, err := placeFile(src, dest); err != nil || changed {
		t.Fatalf("second placeFile = %v, %v", changed, err)
	}
}

func TestPlaceWindowsKeepsPathsAndFilesThatAreNotOurs(t *testing.T) {
	l := windowsTestLayout(t)
	src := t.TempDir()
	writeWindowsRelease(t, src)
	reg := newFakeRegistry()
	reg.path = `%LOCALAPPDATA%\agent-pane\bin\`
	if runtime.GOOS != "windows" {
		reg.path = `%LOCALAPPDATA%` + string(filepath.Separator) + filepath.Join("agent-pane", "bin")
	}
	theirs := "# My notes\n"
	putFile(t, filepath.Join(l.home, "AGENTS.md"), theirs)
	putFile(t, filepath.Join(l.home, ".claude", "skills", "agent-pane", "SKILL.md"), "my own skill\n")

	_, errOut := placeOnce(t, l, src, reg)
	if reg.pathSet {
		t.Errorf("user Path rewritten although it names the bin folder: %q", reg.path)
	}
	if got := readString(t, filepath.Join(l.home, "AGENTS.md")); got != theirs {
		t.Errorf("~/AGENTS.md without a block changed to %q", got)
	}
	if got := readString(t, filepath.Join(l.home, ".claude", "skills", "agent-pane", "SKILL.md")); got != "my own skill\n" {
		t.Errorf("a skill folder that is not ours changed to %q", got)
	}
	for _, want := range []string{"AGENTS.md has no Agent Pane block; left unchanged", "is not Agent Pane's; left unchanged"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}
}

func TestPlaceWindowsRefuses(t *testing.T) {
	t.Run("no browser", func(t *testing.T) {
		root := t.TempDir()
		l := layoutFor("windows", root, filepath.Join(root, "Local"))
		src := t.TempDir()
		writeWindowsRelease(t, src)
		p, _, _ := testPlacer(t, l, src, newFakeRegistry())
		if err := p.run(); err == nil || !strings.Contains(err.Error(), "no browser") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("incomplete release", func(t *testing.T) {
		l := windowsTestLayout(t)
		src := t.TempDir()
		writeWindowsRelease(t, src)
		os.RemoveAll(filepath.Join(src, "licenses", "herdr-core", "THIRD-PARTY-NOTICES"))
		putFile(t, filepath.Join(src, "licenses", "herdr-core", "THIRD-PARTY-NOTICES", "empty.txt"), "")
		p, _, _ := testPlacer(t, l, src, newFakeRegistry())
		if err := p.run(); err == nil || !strings.Contains(err.Error(), "third-party notices") {
			t.Fatalf("err = %v", err)
		}
		if exists(l.release) {
			t.Error("an incomplete release was kept")
		}
	})
	t.Run("start marker without end", func(t *testing.T) {
		l := windowsTestLayout(t)
		src := t.TempDir()
		writeWindowsRelease(t, src)
		putFile(t, filepath.Join(l.home, "CLAUDE.md"), managedStart+"\n")
		p, _, _ := testPlacer(t, l, src, newFakeRegistry())
		if err := p.run(); err == nil || !strings.Contains(err.Error(), "no end marker") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("doctor fails", func(t *testing.T) {
		l := windowsTestLayout(t)
		src := t.TempDir()
		writeWindowsRelease(t, src)
		p, out, _ := testPlacer(t, l, src, newFakeRegistry())
		p.runInstalled = func(...string) (string, error) { return "FAIL  core\n", os.ErrInvalid }
		if err := p.run(); err == nil || !strings.Contains(out.String(), "FAIL  core") {
			t.Fatalf("err = %v, output %q", err, out)
		}
	})
}

func TestReplaceManagedBlock(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"block in the middle",
			"before\n" + managedStart + "\nold\n" + managedEnd + "\nafter\n",
			"before\n" + managedStart + "\nnew\n" + managedEnd + "\nafter\n"},
		{"no trailing newline",
			managedStart + "\nold\n" + managedEnd,
			managedStart + "\nnew\n" + managedEnd + "\n"},
		{"CRLF file",
			"before\r\n" + managedStart + "\r\nold\r\n" + managedEnd + "\r\nafter\r\n",
			"before\r\n" + managedStart + "\nnew\n" + managedEnd + "\nafter\r\n"},
		{"two blocks",
			managedStart + "\na\n" + managedEnd + "\nmid\n" + managedStart + "\nb\n" + managedEnd + "\n",
			managedStart + "\nnew\n" + managedEnd + "\nmid\n" + managedStart + "\nnew\n" + managedEnd + "\n"},
		{"marker inside a line is not a block",
			"x " + managedStart + "\n",
			"x " + managedStart + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := replaceManagedBlock(tc.in, managedStart+"\nnew\n"+managedEnd); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathHelpers(t *testing.T) {
	getenv := func(name string) string {
		if name == "LOCALAPPDATA" {
			return `C:\Users\me\AppData\Local`
		}
		return ""
	}
	if got := expandWindowsEnv(`%LOCALAPPDATA%\agent-pane\bin;%NOPE%`, getenv); got != `C:\Users\me\AppData\Local\agent-pane\bin;%NOPE%` {
		t.Errorf("expandWindowsEnv = %q", got)
	}
	if !samePath("windows", `C:\Users\Me\bin\`, `c:\users\me\bin`) {
		t.Error("samePath on Windows is not case-insensitive or keeps trailing separators")
	}
	if samePath("linux", "/home/Me", "/home/me") {
		t.Error("samePath on Linux ignores case")
	}
}

func TestPlaceCommandRefusesOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("places on Windows")
	}
	c := NewCmdPlace()
	c.SetArgs(nil)
	c.SetOut(new(strings.Builder))
	c.SetErr(new(strings.Builder))
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "only used on Windows") {
		t.Fatalf("err = %v", err)
	}
}
