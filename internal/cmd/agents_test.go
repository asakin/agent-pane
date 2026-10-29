package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgent(t *testing.T, dir, id, body string) {
	t.Helper()
	folder := filepath.Join(dir, id)
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "agent.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAgentsMergeAndEnabled(t *testing.T) {
	root := t.TempDir()
	shipped := filepath.Join(root, "shipped")
	user := filepath.Join(root, "user")
	writeAgent(t, shipped, "claude", `{"name":"Claude","command":"claude"}`)
	writeAgent(t, shipped, "goose", `{"name":"Goose","command":"goose"}`)
	writeAgent(t, shipped, "broken", `{`)
	writeAgent(t, shipped, "empty", `{"name":"Empty"}`)
	writeAgent(t, user, "claude", `{"name":"Mine","command":"my-claude"}`)
	if err := os.WriteFile(filepath.Join(user, "enabled.json"), []byte(`{"agents":{"claude":{"enabled":false},"goose":{"enabled":false}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shipped, "enabled.json"), []byte(`{"agents":{"claude":{"enabled":true},"goose":{"enabled":true},"missing":{"enabled":true}}}`), 0644); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"my-claude", "goose"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	untouched := loadAgents(shipped, user, nil, binDir)
	for _, agent := range untouched {
		if agent.ID == "claude" && !agent.Enabled {
			t.Fatal("user enabled.json turned claude off")
		}
		if agent.ID == "goose" && !agent.Enabled {
			t.Fatal("user enabled.json turned goose off")
		}
	}

	off := false
	got := loadAgents(shipped, user, map[string]*bool{"goose": &off}, binDir)
	byID := map[string]Agent{}
	for _, agent := range got {
		byID[agent.ID] = agent
	}
	if _, ok := byID["broken"]; ok {
		t.Fatal("broken agent.json was not skipped")
	}
	if _, ok := byID["empty"]; ok {
		t.Fatal("agent without command was not skipped")
	}
	if _, ok := byID["missing"]; ok {
		t.Fatal("enabled.json invented an agent with no folder")
	}
	claude := byID["claude"]
	if claude.Name != "Mine" || claude.Source != "user" || !claude.Enabled || !claude.Available {
		t.Fatalf("claude = %+v", claude)
	}
	if !strings.HasSuffix(claude.Bin, "my-claude") {
		t.Fatalf("bin = %s", claude.Bin)
	}
	goose := byID["goose"]
	if goose.Enabled || goose.Source != "shipped" || !goose.Available {
		t.Fatalf("goose = %+v", goose)
	}
}

func TestShippedEnabledBrokenIsOff(t *testing.T) {
	root := t.TempDir()
	writeAgent(t, root, "claude", `{"name":"Claude","command":"claude"}`)
	if err := os.WriteFile(filepath.Join(root, "enabled.json"), []byte(`{`), 0644); err != nil {
		t.Fatal(err)
	}
	got := loadAgents(root, filepath.Join(root, "nope"), nil, "")
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("agents = %+v", got)
	}
}

func TestAgentExecArgsDoesNotTrap(t *testing.T) {
	argv := agentExecArgs("/bin/zsh", []string{"/opt/homebrew/bin/claude", "--resume"})
	script := strings.Join(argv, "\n")
	if strings.Contains(script, "trap") || strings.Contains(script, `exec "$shell"`) {
		t.Fatalf("argv = %#v", argv)
	}
	if argv[0] != "-l" || argv[1] != "-c" || argv[2] != `exec "$0" "$@"` || argv[3] != "/opt/homebrew/bin/claude" || argv[4] != "--resume" {
		t.Fatalf("argv = %#v", argv)
	}
	fish := agentExecArgs("/usr/local/bin/fish", []string{"/bin/claude"})
	if fish[0] != "-l" || fish[1] != "-c" || fish[2] != "exec $argv" || fish[3] != "/bin/claude" {
		t.Fatalf("fish = %#v", fish)
	}
}

func TestLoginPathArgs(t *testing.T) {
	zsh := loginPathArgs("/bin/zsh")
	if strings.Join(zsh, " ") != "-lc printenv PATH" {
		t.Fatalf("zsh = %#v", zsh)
	}
	fish := loginPathArgs("/usr/local/bin/fish")
	if strings.Join(fish, " ") != "-l -c printenv PATH" {
		t.Fatalf("fish = %#v", fish)
	}
}

func TestResolveCommandRelativeToFolder(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "bin", "wrap")
	if err := os.MkdirAll(filepath.Dir(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveCommand(dir, "bin/wrap", nil)
	if !ok || got != wrapper {
		t.Fatalf("got %q %v", got, ok)
	}
}
