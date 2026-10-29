package cmd

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	envMarkerStart  = "AGENT_TERMINAL_ENV_START"
	envMarkerEnd    = "AGENT_TERMINAL_ENV_END"
	shellEnvTimeout = 20 * time.Second
)

// shellCaptureArgs is how a login interactive shell prints its environment.
// bash and zsh need both -l and -i: login files and interactive files are
// different, and either one may export PATH. fish has its own flags.
func shellCaptureArgs(shell string) ([]string, bool) {
	script := "printf '%s\\n' '" + envMarkerStart + "'\ncommand env -0\nprintf '\\n%s\\n' '" + envMarkerEnd + "'\n"
	switch filepath.Base(shell) {
	case "bash", "zsh":
		return []string{"-ilc", script}, true
	case "fish":
		return []string{"-l", "-c", script}, true
	default:
		return nil, false
	}
}

func parseShellEnv(out []byte) ([]string, error) {
	start := []byte(envMarkerStart + "\n")
	end := []byte("\n" + envMarkerEnd)
	i := bytes.Index(out, start)
	if i < 0 {
		return nil, fmt.Errorf("shell environment had no start marker")
	}
	rest := out[i+len(start):]
	j := bytes.Index(rest, end)
	if j < 0 {
		return nil, fmt.Errorf("shell environment had no end marker")
	}
	var env []string
	for _, part := range bytes.Split(rest[:j], []byte{0}) {
		if len(part) == 0 || !bytes.Contains(part, []byte{'='}) {
			continue
		}
		env = append(env, string(part))
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("shell environment was empty")
	}
	return env, nil
}

func captureShellEnv(shell string) ([]string, error) {
	args, ok := shellCaptureArgs(shell)
	if !ok || shell == "" {
		return nil, fmt.Errorf("shell %q does not load a user environment", shell)
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellEnvTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, args...)
	out, err := cmd.Output()
	env, parseErr := parseShellEnv(out)
	if parseErr != nil {
		if err != nil {
			return nil, fmt.Errorf("shell environment: %w", err)
		}
		return nil, parseErr
	}
	return env, nil
}

var (
	userEnvOnce sync.Once
	userEnv     []string
)

// userShellEnv is the environment a login interactive $SHELL would have.
// The host is started by Chrome, so its own environment is not that.
// Capture runs once per process. A failure keeps the process environment.
func userShellEnv() []string {
	userEnvOnce.Do(func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = getDefaultShell()
		}
		env, err := captureShellEnv(shell)
		if err != nil {
			log.Printf("shell environment: %v", err)
			userEnv = os.Environ()
			return
		}
		userEnv = env
	})
	return userEnv
}
