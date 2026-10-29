package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const agentIconMaxBytes = 512 << 10

// Agent is one folder the host could launch.
type Agent struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Args      []string `json:"-"`
	Enabled   bool     `json:"enabled"`
	Available bool     `json:"available"`
	Source    string   `json:"source"`
	Icon      string   `json:"icon"`
	Bin       string   `json:"-"`
	Dir       string   `json:"-"`
}

type agentFile struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func catalog() []Agent {
	overrides := map[string]*bool{}
	if cfg, err := readConfigMap(); err == nil {
		overrides = agentEnabledOverrides(cfg)
	}
	return loadAgents(shippedAgentsDir, userAgentsDir, overrides, loginPATH())
}

func agentByID(id string) (Agent, bool) {
	for _, agent := range catalog() {
		if agent.ID == id && agent.Available {
			return agent, true
		}
	}
	return Agent{}, false
}

// agentEnabledOverrides is config.json agents.<id>.enabled when that key exists.
func agentEnabledOverrides(cfg map[string]any) map[string]*bool {
	raw, _ := cfg["agents"].(map[string]any)
	out := map[string]*bool{}
	for id, value := range raw {
		obj, ok := value.(map[string]any)
		if !ok {
			continue
		}
		enabled, ok := obj["enabled"].(bool)
		if !ok {
			continue
		}
		bit := enabled
		out[id] = &bit
	}
	return out
}

// loadAgents reads shipped folders, then user folders (user replaces that id).
// overrides win over enabled.json. A listed id with no valid folder is omitted.
func loadAgents(shippedDir, userDir string, overrides map[string]*bool, path string) []Agent {
	defaults := shippedEnabled(filepath.Join(shippedDir, "enabled.json"))
	byID := map[string]Agent{}
	readAgentDir(shippedDir, "shipped", byID)
	readAgentDir(userDir, "user", byID)

	pathEnv := []string{"PATH=" + path}
	out := make([]Agent, 0, len(byID))
	for id, agent := range byID {
		if bit, ok := overrides[id]; ok {
			agent.Enabled = *bit
		} else {
			agent.Enabled = defaults[id]
		}
		agent.Bin, agent.Available = resolveCommand(agent.Dir, agent.Command, pathEnv)
		out = append(out, agent)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// shippedEnabled returns ids whose enabled bit is true.
// A missing or invalid file means every id is off.
func shippedEnabled(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]bool{}
	}
	var file struct {
		Agents map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Agents == nil {
		return map[string]bool{}
	}
	out := map[string]bool{}
	for id, entry := range file.Agents {
		if entry.Enabled != nil && *entry.Enabled {
			out[id] = true
		}
	}
	return out
}

func readAgentDir(dir, source string, into map[string]Agent) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !isSafeAppID(id) {
			continue
		}
		agent, ok := readAgent(filepath.Join(dir, id), id, source)
		if !ok {
			continue
		}
		into[id] = agent
	}
}

func readAgent(dir, id, source string) (Agent, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err != nil {
		return Agent{}, false
	}
	var file agentFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Agent{}, false
	}
	command := strings.TrimSpace(file.Command)
	if command == "" {
		return Agent{}, false
	}
	name := strings.TrimSpace(file.Name)
	if name == "" {
		name = id
	}
	return Agent{
		ID:      id,
		Name:    name,
		Command: command,
		Args:    append([]string{}, file.Args...),
		Source:  source,
		Icon:    agentIconDataURL(dir),
		Dir:     dir,
	}, true
}

func agentIconDataURL(dir string) string {
	for _, name := range []string{"icon.svg", "icon.png"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > agentIconMaxBytes {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		mime := "image/svg+xml"
		if name == "icon.png" {
			mime = "image/png"
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return ""
}

// resolveCommand finds command on pathEnv, or inside dir when it is a path.
func resolveCommand(dir, command string, pathEnv []string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", false
	}
	var candidate string
	if filepath.IsAbs(command) {
		candidate = command
	} else if strings.ContainsAny(command, `/\`) {
		candidate = filepath.Join(dir, command)
	} else if bin, ok := lookPathIn(pathEnv, command); ok {
		return bin, true
	} else {
		return "", false
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return "", false
	}
	return candidate, true
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

// lookPathIn finds name in env's PATH. name must be a single path element.
func lookPathIn(env []string, name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	for _, dir := range filepath.SplitList(envValue(env, "PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		return candidate, true
	}
	return "", false
}

func configuredShell() string {
	shell := k.String("command")
	if shell == "" {
		return getDefaultShell()
	}
	return shell
}

// agentExecArgs starts a login shell only long enough to exec argv.
// The script does not trap signals and does not start another shell after exit.
func agentExecArgs(shell string, argv []string) []string {
	if filepath.Base(shell) == "fish" {
		out := []string{"-l", "-c", "exec $argv"}
		return append(out, argv...)
	}
	out := []string{"-l", "-c", `exec "$0" "$@"`}
	return append(out, argv...)
}

var (
	loginPathOnce sync.Once
	loginPathVal  string
)

// loginPATH is the PATH a login shell would use. The host's own PATH is Chrome's.
func loginPATH() string {
	loginPathOnce.Do(func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = getDefaultShell()
		}
		loginPathVal = readLoginPATH(shell)
	})
	return loginPathVal
}

func loginPathArgs(shell string) []string {
	if filepath.Base(shell) == "fish" {
		return []string{"-l", "-c", "printenv PATH"}
	}
	return []string{"-lc", "printenv PATH"}
}

func readLoginPATH(shell string) string {
	if shell == "" {
		return os.Getenv("PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, loginPathArgs(shell)...).Output()
	if err != nil {
		return os.Getenv("PATH")
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return os.Getenv("PATH")
	}
	return path
}
