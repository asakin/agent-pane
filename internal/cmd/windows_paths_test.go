package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUserDirsOnWindows(t *testing.T) {
	env := map[string]string{
		"HOME":         "/home/me",
		"USERPROFILE":  `C:\Users\me`,
		"LOCALAPPDATA": `C:\Users\me\AppData\Local`,
	}
	getenv := func(k string) string { return env[k] }
	if got := userHome("windows", getenv); got != `C:\Users\me` {
		t.Fatalf("home = %q", got)
	}
	if got := userDataDir("windows", getenv); got != filepath.Join(`C:\Users\me\AppData\Local`, "agent-pane") {
		t.Fatalf("data = %q", got)
	}
	if got := userCacheDir("windows", getenv); got != filepath.Join(`C:\Users\me\AppData\Local`, "agent-pane", "cache") {
		t.Fatalf("cache = %q", got)
	}
	if got := userDataDir("linux", getenv); got != filepath.Join("/home/me", ".local", "share", "agent-pane") {
		t.Fatalf("linux data = %q", got)
	}
}

func TestWindowsDefaultShell(t *testing.T) {
	found := func(string) (string, error) { return `C:\Program Files\PowerShell\7\pwsh.exe`, nil }
	if got := windowsDefaultShell(found); got != `C:\Program Files\PowerShell\7\pwsh.exe` {
		t.Fatalf("with pwsh = %q", got)
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got := windowsDefaultShell(missing); got != "powershell.exe" {
		t.Fatalf("without pwsh = %q", got)
	}
}

func TestHostArgs(t *testing.T) {
	chrome := []string{"chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/", "--parent-window=0"}
	if got := HostArgs(chrome); !slices.Equal(got, []string{"serve"}) {
		t.Fatalf("browser start = %q", got)
	}
	if got := HostArgs([]string{"doctor", "--fix"}); !slices.Equal(got, []string{"doctor", "--fix"}) {
		t.Fatalf("doctor = %q", got)
	}
	if got := HostArgs(nil); len(got) != 0 {
		t.Fatalf("none = %q", got)
	}
}

func TestFindExecutableUsesPATHEXTOnWindows(t *testing.T) {
	dir := t.TempDir()
	// npm installs a .cmd shim and an extensionless sh script side by side.
	for _, name := range []string{"claude", "claude.cmd", "pi.ps1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := findExecutable("windows", filepath.Join(dir, "claude"), ".COM;.EXE;.BAT;.CMD")
	if !ok || got != filepath.Join(dir, "claude.cmd") {
		t.Fatalf("claude = %q %v", got, ok)
	}
	got, ok = findExecutable("windows", filepath.Join(dir, "claude.cmd"), ".COM;.EXE;.BAT;.CMD")
	if !ok || got != filepath.Join(dir, "claude.cmd") {
		t.Fatalf("claude.cmd = %q %v", got, ok)
	}
	if got, ok := findExecutable("windows", filepath.Join(dir, "pi"), ""); ok {
		t.Fatalf("a .ps1 script is not run directly, got %q", got)
	}
}

func TestConsoleDevice(t *testing.T) {
	if consoleDevice("windows") != "CONOUT$" || consoleDevice("linux") != "/dev/tty" {
		t.Fatal("console device")
	}
}
