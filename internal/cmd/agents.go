package cmd

import (
	"os"
	"path/filepath"
	"strings"
)

// agentCommands are started by name. The path is whatever the user's shell
// PATH says at launch. cursor-agent is not listed.
var agentCommands = []string{"pi", "opencode", "goose", "claude", "codex"}

const agentLaunchScript = "trap '' INT\n" +
	"bin=$1\n" +
	"shell=$2\n" +
	"shift 2\n" +
	"\"$bin\" \"$@\"\n" +
	"trap - INT\n" +
	"exec \"$shell\"\n"

func isAgentCommand(name string) bool {
	for _, cmd := range agentCommands {
		if cmd == name {
			return true
		}
	}
	return false
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

// visibleApps adds catalog commands the shell can run that are not already
// a script in apps/.
func visibleApps(files []string, env []string) []string {
	seen := make(map[string]struct{}, len(files))
	out := append([]string{}, files...)
	for _, name := range files {
		seen[name] = struct{}{}
	}
	for _, name := range agentCommands {
		if _, ok := seen[name]; ok {
			continue
		}
		if _, ok := lookPathIn(env, name); !ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// agentLaunchArgv runs bin, then replaces the process with shell.
// bin is an argument, resolved by the caller at launch.
func agentLaunchArgv(bin, shell string, args []string) []string {
	argv := []string{"-c", agentLaunchScript, "_", bin, shell}
	return append(argv, args...)
}

func configuredShell() string {
	shell := k.String("command")
	if shell == "" {
		return getDefaultShell()
	}
	return shell
}
