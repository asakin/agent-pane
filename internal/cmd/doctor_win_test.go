package cmd

// Doctor on the Windows layout. These run on every OS: the install is placed
// under a temporary folder by the placing step with a map registry.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// healthyWindowsInstall places a Windows release into a temporary layout.
func healthyWindowsInstall(t *testing.T) (installLayout, *fakeRegistry) {
	t.Helper()
	l := windowsTestLayout(t)
	src := t.TempDir()
	writeWindowsRelease(t, src)
	reg := newFakeRegistry()
	placeOnce(t, l, src, reg)
	return l, reg
}

func TestDoctorWindowsHealthyInstall(t *testing.T) {
	l, reg := healthyWindowsInstall(t)
	results := runChecks(l, windowsTestEnv(l, reg))
	for _, r := range results {
		if r.status != checkOK {
			t.Errorf("%s %s: %s", r.status, r.item, r.detail)
		}
	}
	items := statuses(results)
	for _, item := range []string{"release", "binary", "native host", "manifest", "core", "core licenses", "core isolation", "PATH", "instructions", "agent catalog", "skill", "skill copy", "extension"} {
		if items[item] != checkOK {
			t.Errorf("no ok %s check", item)
		}
	}
	if _, ok := items["spawn agent"]; ok {
		t.Error("the spawn agent is checked on Windows")
	}
	// Paths are shown relative to the Windows folders.
	for _, r := range results {
		if strings.Contains(r.detail, l.localAppData) {
			t.Errorf("%s detail shows the full path: %s", r.item, r.detail)
		}
	}
}

func TestDoctorWindowsFindsProblems(t *testing.T) {
	libexec := func(l installLayout, rel string) string {
		return filepath.Join(l.dataDir, "libexec", filepath.FromSlash(rel))
	}
	cases := []struct {
		name   string
		item   string
		want   checkStatus
		env    func(t *testing.T, l installLayout, e *doctorEnv)
		damage func(t *testing.T, l installLayout, reg *fakeRegistry)
	}{
		{name: "no release binary", item: "release", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			os.Remove(filepath.Join(l.release, "agent-pane.exe"))
		}},
		{name: "binary differs", item: "binary", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, l.binary, "old binary")
		}},
		{name: "manifest file names another program", item: "native host", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, l.manifest, string(windowsManifest(`C:\elsewhere\agent-pane.exe`)))
		}},
		{name: "manifest file missing", item: "native host", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			os.Remove(l.manifest)
		}},
		{name: "browser key missing", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout, reg *fakeRegistry) {
			delete(reg.values, browserDirs("windows", l.home, l.localAppData)[0].key)
		}},
		{name: "browser key names another file", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout, reg *fakeRegistry) {
			reg.values[browserDirs("windows", l.home, l.localAppData)[0].key] = `C:\other.json`
		}},
		{name: "browser installed after placing", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			os.MkdirAll(browserDirs("windows", l.home, l.localAppData)[3].parent, 0o755)
		}},
		{name: "no browser", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			os.RemoveAll(browserDirs("windows", l.home, l.localAppData)[0].parent)
		}},
		{name: "core differs", item: "core", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, l.core, "other core")
		}},
		{name: "conpty.dll differs", item: "core", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, libexec(l, "conpty/conpty.dll"), "other dll")
		}},
		{name: "OpenConsole.exe missing", item: "core", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			os.Remove(libexec(l, "conpty/x64/OpenConsole.exe"))
		}},
		{name: "core reports another version", item: "core", want: checkFail, env: func(_ *testing.T, _ installLayout, e *doctorEnv) {
			e.coreVersion = func(string) (string, error) { return "hc 0.1.18", nil }
		}},
		{name: "empty notice in the notices folder", item: "core licenses", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			for _, dir := range []string{l.release, l.dataDir} {
				putFile(t, filepath.Join(dir, "licenses", "herdr-core", "THIRD-PARTY-NOTICES", "empty.txt"), "")
			}
		}},
		{name: "notices as one file", item: "core licenses", want: checkOK, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			for _, dir := range []string{l.release, l.dataDir} {
				notices := filepath.Join(dir, "licenses", "herdr-core", "THIRD-PARTY-NOTICES")
				os.RemoveAll(notices)
				putFile(t, notices, "notices\n")
			}
		}},
		{name: "core libexec on Path", item: "core isolation", want: checkFail, env: func(_ *testing.T, l installLayout, e *doctorEnv) {
			e.path = `C:\Windows;` + filepath.Dir(l.core)
		}},
		{name: "release libexec on Path", item: "core isolation", want: checkFail, env: func(_ *testing.T, l installLayout, e *doctorEnv) {
			e.path = filepath.Dir(l.releaseCore()) + `;C:\Windows`
		}},
		{name: "a person's own hc.cmd is left alone", item: "core isolation", want: checkOK, env: func(t *testing.T, _ installLayout, e *doctorEnv) {
			dir := t.TempDir()
			putFile(t, filepath.Join(dir, "hc.cmd"), "@echo their hc")
			e.path = dir
		}},
		{name: "bin on the process Path", item: "PATH", want: checkOK, env: func(_ *testing.T, l installLayout, e *doctorEnv) {
			e.path = `C:\Windows;` + filepath.Dir(l.binary)
		}},
		{name: "bin not on the user Path", item: "PATH", want: checkWarn, damage: func(t *testing.T, _ installLayout, reg *fakeRegistry) {
			reg.path = `C:\Windows`
		}},
		{name: "another agent-pane first on Path", item: "PATH", want: checkWarn, env: func(t *testing.T, l installLayout, e *doctorEnv) {
			dir := t.TempDir()
			putFile(t, filepath.Join(dir, "agent-pane.exe"), "x")
			e.path = dir + ";" + filepath.Dir(l.binary)
		}},
		{name: "skill copy stale", item: "skill copy", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, filepath.Join(l.home, ".claude", "skills", "agent-pane", "SKILL.md"), "stale")
		}},
		{name: "instructions changed", item: "instructions", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, filepath.Join(l.configDir, "AGENTS.md"), "")
		}},
		{name: "extension stale", item: "extension", want: checkFail, damage: func(t *testing.T, l installLayout, _ *fakeRegistry) {
			putFile(t, filepath.Join(l.dataDir, "extensions", "chrome", "background.js"), "old")
		}},
		{name: "versions differ", item: "versions", want: checkFail, env: func(_ *testing.T, _ installLayout, e *doctorEnv) {
			e.version = "0.4.0"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, reg := healthyWindowsInstall(t)
			env := windowsTestEnv(l, reg)
			if tc.env != nil {
				tc.env(t, l, &env)
			}
			if tc.damage != nil {
				tc.damage(t, l, reg)
			}
			results := runChecks(l, env)
			if got := statuses(results)[tc.item]; got != tc.want {
				t.Fatalf("%s: got %q, want %q: %+v", tc.item, got, tc.want, results)
			}
		})
	}
}

func TestDoctorWindowsFixNeedsARelease(t *testing.T) {
	l := windowsTestLayout(t)
	err := runPlace(l, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "install.ps1") {
		t.Fatalf("runPlace = %v, want an error naming install.ps1", err)
	}
	results := runChecks(l, windowsTestEnv(l, newFakeRegistry()))
	if len(results) != 1 || results[0].item != "release" || !strings.Contains(results[0].detail, "irm ") {
		t.Fatalf("got %+v, want one failed release check with the PowerShell install command", results)
	}
}

func TestReadCoreLockFileLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "herdr-core.lock")
	putFile(t, path, winTestLock())
	lock, err := readCoreLock(path)
	if err != nil {
		t.Fatal(err)
	}
	files := lock.files["windows_amd64"]
	if lock.binaries["windows_amd64"] != sha(winTestCore) || len(files) != 2 ||
		files[0] != (coreFile{"conpty/conpty.dll", sha(winTestConpty)}) ||
		files[1] != (coreFile{"conpty/x64/OpenConsole.exe", sha(winTestConsole)}) {
		t.Fatalf("lock = %+v", lock)
	}
	if len(lock.files["linux_amd64"]) != 0 {
		t.Errorf("linux_amd64 has files: %+v", lock.files["linux_amd64"])
	}
	for _, bad := range []string{"../hc.exe", "/abs", `conpty\conpty.dll`} {
		putFile(t, path, "version 1\nwindows_amd64.file "+bad+" 00\n")
		if _, err := readCoreLock(path); err == nil {
			t.Errorf("readCoreLock accepted the file path %q", bad)
		}
	}
}

func TestPathextNames(t *testing.T) {
	got := strings.Join(pathextNames("hc", ".COM;.EXE;;.cmd"), " ")
	if want := "hc.COM hc.com hc.EXE hc.exe hc.cmd"; got != want {
		t.Errorf("pathextNames = %q, want %q", got, want)
	}
}
