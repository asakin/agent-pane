package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseShellEnvIgnoresBanner(t *testing.T) {
	out := []byte("starship: command not found\n" +
		envMarkerStart + "\n" +
		"PATH=/opt/homebrew/bin:/usr/bin\x00HOME=/Users/a\x00" +
		"\n" + envMarkerEnd + "\n")
	env, err := parseShellEnv(out)
	if err != nil {
		t.Fatal(err)
	}
	if envValue(env, "PATH") != "/opt/homebrew/bin:/usr/bin" {
		t.Fatalf("PATH = %q", envValue(env, "PATH"))
	}
	if envValue(env, "HOME") != "/Users/a" {
		t.Fatalf("HOME = %q", envValue(env, "HOME"))
	}
}

func TestShellCaptureArgs(t *testing.T) {
	args, ok := shellCaptureArgs("/bin/zsh")
	if !ok || len(args) != 2 || args[0] != "-ilc" {
		t.Fatalf("zsh: %v %v", ok, args)
	}
	args, ok = shellCaptureArgs("/usr/local/bin/fish")
	if !ok || args[0] != "-l" || args[1] != "-c" {
		t.Fatalf("fish: %v %v", ok, args)
	}
	if _, ok := shellCaptureArgs("/bin/dash"); ok {
		t.Fatal("dash should not be treated as a login interactive shell")
	}
}

func TestCaptureShellEnvFromFakeZsh(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "zsh")
	script := "#!/bin/sh\nprintf '%s\\n' '" + envMarkerStart + "'\nprintf 'FOO=bar\\0PATH=/agents:/bin\\0'\nprintf '\\n%s\\n' '" + envMarkerEnd + "'\n"
	if err := os.WriteFile(shell, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	env, err := captureShellEnv(shell)
	if err != nil {
		t.Fatal(err)
	}
	if envValue(env, "FOO") != "bar" || envValue(env, "PATH") != "/agents:/bin" {
		t.Fatalf("env = %v", env)
	}
}

func TestVisibleAppsUsesPATHNotAStoredPath(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + dir + ":/usr/bin"}
	got := visibleApps(nil, env)
	if len(got) != 1 || got[0] != "pi" {
		t.Fatalf("apps = %v", got)
	}
	if _, ok := lookPathIn(env, "cursor-agent"); ok {
		t.Fatal("cursor-agent is not an agent command")
	}
	missing := visibleApps(nil, []string{"PATH=/usr/bin"})
	if len(missing) != 0 {
		t.Fatalf("expected no apps, got %v", missing)
	}
}

func TestVisibleAppsKeepsAScriptAndDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	got := visibleApps([]string{"codex", "mine"}, []string{"PATH=" + dir})
	if strings.Join(got, ",") != "codex,mine" {
		t.Fatalf("apps = %v", got)
	}
}

func TestAgentLaunchArgvResolvesAtLaunch(t *testing.T) {
	argv := agentLaunchArgv("/opt/homebrew/bin/claude", "/bin/bash", []string{"--resume"})
	if argv[0] != "-c" || argv[2] != "_" || argv[3] != "/opt/homebrew/bin/claude" || argv[4] != "/bin/bash" || argv[5] != "--resume" {
		t.Fatalf("argv = %#v", argv)
	}
	if !strings.Contains(argv[1], `exec "$shell"`) {
		t.Fatalf("script = %s", argv[1])
	}
}
