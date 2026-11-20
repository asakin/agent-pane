package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsShellPath(t *testing.T) {
	dir, other := t.TempDir(), t.TempDir()
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := other + ";" + dir
	for shell, want := range map[string]string{
		"pwsh":           filepath.Join(dir, "pwsh.exe"),
		"PWSH.exe":       filepath.Join(dir, "PWSH.exe"),
		"powershell.exe": filepath.Join(dir, "powershell.exe"),
		`D:\tools\x.exe`: `D:\tools\x.exe`,
		"nushell":        "nushell",
	} {
		if got := windowsShellPath(shell, path, ".COM;.EXE", `C:\Windows`); got != want {
			t.Errorf("%s = %q, want %q", shell, got, want)
		}
	}
	fallback := filepath.Join(`D:\Win`, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for _, shell := range []string{"powershell.exe", "PowerShell"} {
		if got := windowsShellPath(shell, other, ".COM;.EXE", `D:\Win`); got != fallback {
			t.Errorf("%s with no PowerShell on PATH = %q, want %q", shell, got, fallback)
		}
	}
}

// A bare shell name must reach go-pty as an absolute path: given a bare name
// and a working folder, it looks only in that folder.
func TestBuildTerminalCommandResolvesBareShellOnWindows(t *testing.T) {
	cfg().Set("command", "cmd.exe")
	t.Cleanup(func() { cfg().Delete("command") })
	cmd, err := buildTerminalCommand("", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(cmd.Path) || !samePath("windows", filepath.Base(cmd.Path), "cmd.exe") {
		t.Fatalf("shell = %q, want an absolute cmd.exe", cmd.Path)
	}
	if _, err := os.Stat(cmd.Path); err != nil {
		t.Fatal(err)
	}
}
