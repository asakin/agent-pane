package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewCmdRoot("test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func useSkillPath(t *testing.T, path string) {
	t.Helper()
	old := skillPath
	skillPath = path
	t.Cleanup(func() { skillPath = old })
}

func TestSkillFlagPrintsTheInstalledSkill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	want := "---\nname: agent-pane\n---\n\n# Agent Pane\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	useSkillPath(t, path)
	// No config directory is needed or written.
	oldConfig := configDir
	configDir = filepath.Join(t.TempDir(), "unused")
	t.Cleanup(func() { configDir = oldConfig })

	out, _, err := runRoot(t, "--skill")
	if err != nil {
		t.Fatal(err)
	}
	if out != want {
		t.Fatalf("printed %q, want %q", out, want)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("--skill touched the config directory: %v", err)
	}
}

func TestSkillFlagFailsClearlyWhenNotInstalled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "SKILL.md")
	useSkillPath(t, path)

	out, _, err := runRoot(t, "--skill")
	if err == nil {
		t.Fatal("no error for a missing skill")
	}
	if !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not say the skill is missing and where", err)
	}
	if out != "" {
		t.Fatalf("printed %q", out)
	}
}
