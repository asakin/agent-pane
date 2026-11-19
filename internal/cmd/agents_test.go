package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

func TestSessionDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := sessionDir("/tmp/asked", home+"/agents", true); got != "/tmp/asked" {
		t.Fatalf("request = %s", got)
	}
	if got := sessionDir("", "~/work", true); got != filepath.Join(home, "work") {
		t.Fatalf("tilde = %s", got)
	}
	if got := sessionDir("", "~/work", false); got != home {
		t.Fatalf("shell = %s", got)
	}
	if got := sessionDir("", "", true); got != home {
		t.Fatalf("empty = %s", got)
	}
}

func TestChooseFolderCancelIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("osascript"); err != nil {
		t.Skip("osascript is macOS-only")
	}
	script := `try
	error "User canceled." number -128
on error errMsg number errNum
	if errNum is -128 then
		return ""
	end if
	error errMsg number errNum
end try`
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	got, pickErr := folderPick(out, err)
	if pickErr != nil || got != "" {
		t.Fatalf("cancel = %q err=%v pick=%v", out, err, pickErr)
	}
}

func TestFolderPick(t *testing.T) {
	got, err := folderPick([]byte("/Users/a/proj\n"), nil)
	if err != nil || got != "/Users/a/proj" {
		t.Fatalf("path = %q %v", got, err)
	}
	got, err = folderPick(nil, fmt.Errorf("osascript: User canceled. (-128)"))
	if err != nil || got != "" {
		t.Fatalf("cancel = %q %v", got, err)
	}
	if _, err = folderPick(nil, fmt.Errorf("osascript: not allowed")); err == nil {
		t.Fatal("expected a picker error")
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

func TestInitializeReportsVersionAndProtocol(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := initializeHandler(logger, "v1.2.3", nil)

	result, err := handler([]byte(`{"version":"1.2.3","browserId":"abc"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "v1.2.3" {
		t.Fatalf("version = %v", got["version"])
	}
	if got["protocol"] != float64(protocolVersion) || protocolVersion < 1 {
		t.Fatalf("protocol = %v", got["protocol"])
	}
}

func TestInitializeRecordsTheBrowser(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewSessionManager(0, logger)
	if _, err := initializeHandler(logger, "dev", m)([]byte(`{"browserId":"abc"}`)); err != nil {
		t.Fatal(err)
	}
	if got := m.windowLabel("3@r1"); got != "abc:3@r1" {
		t.Fatalf("window label %q", got)
	}
}

func TestTTYDetachOfAnUnknownSessionReportsItGone(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := NewSessionManager(0, logger)
	p := m.paneLocked("")
	p.grant(4, "all")
	if m.DetachWindow("missing", p.id) {
		t.Fatal("an unknown session lives on")
	}
	if p.isAllowed(4) {
		t.Fatal("the closed window's pane tab kept its grant")
	}
}

func TestInitializeRejectsBadParams(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := initializeHandler(logger, "dev", nil)([]byte(`{`)); err == nil {
		t.Fatal("expected error for malformed params")
	}
}

// The Blocked domains list is one config.json key: Options saves it on its
// own and the form's Save, which leaves it out, keeps it.
func TestBlockedDomainsPersistAcrossConfigSaves(t *testing.T) {
	old := configDir
	configDir = t.TempDir()
	t.Cleanup(func() { configDir = old })
	t.Cleanup(unwatchConfig)

	if err := writeConfigMap(map[string]any{"blockedDomains": []any{"bank.example"}}); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigMap(map[string]any{"theme": "Tomorrow Night", "cwd": nil}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfigMap()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(cfg["blockedDomains"]); got != "[bank.example]" {
		t.Fatalf("blockedDomains after a form save: %s", got)
	}
	if err := writeConfigMap(map[string]any{"blockedDomains": []any{}}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = readConfigMap(); fmt.Sprint(cfg["blockedDomains"]) != "[]" {
		t.Fatalf("removing the last entry: %v", cfg["blockedDomains"])
	}
}

// Saves, file watcher reloads and readers can overlap without racing on the
// loaded configuration, and each save is visible to the next reader.
func TestConfigReloadsDoNotRace(t *testing.T) {
	old := configDir
	configDir = t.TempDir()
	t.Cleanup(func() { configDir = old })
	t.Cleanup(unwatchConfig)

	if err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = cfg().String("theme")
			_ = cfg().StringMap("env")
		}
	}()
	for i := 0; i < 20; i++ {
		theme := fmt.Sprintf("theme-%d", i)
		if err := writeConfigMap(map[string]any{"theme": theme}); err != nil {
			t.Fatal(err)
		}
		if got := cfg().String("theme"); got != theme {
			t.Fatalf("theme after save = %q, want %q", got, theme)
		}
	}
	<-done
}
