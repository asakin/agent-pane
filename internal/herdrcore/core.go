// Package herdrcore is a client for the private, pinned herdr-core (hc) that
// Agent Pane bundles. It speaks the core's JSON-RPC protocol on its unix
// socket, in framed mode, so one connection carries calls and terminal
// streams.
//
// The host runs terminals in the core when the Durable (preview) terminal
// backend is chosen (RFC 0001); this package is the bridge's lower half.
package herdrcore

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Generation is the core protocol generation this client speaks, the one
// scripts/herdr-core.lock pins.
const Generation = 1

// Session is the hc session Agent Pane's core runs as, so its socket, state
// and terminals never mix with a person's own Herdr.
const Session = "agent-pane"

// Core is one private herdr-core install: the binary and the paths its
// session lives in.
type Core struct {
	// Binary is the bundled hc, ~/.local/share/agent-pane/libexec/hc.
	Binary string
	// StateDir and ConfigDir are the core's XDG_STATE_HOME and
	// XDG_CONFIG_HOME, so it never reads or writes a person's own
	// ~/.config/herdr-core or state.
	StateDir  string
	ConfigDir string
	// Socket is the core's HC_SOCKET, in a 0700 directory of its own. The
	// core puts its holder sockets beside it.
	Socket string
}

// Private returns the core Agent Pane installs under dataDir
// (~/.local/share/agent-pane), with its state, config and socket under
// dataDir/core.
func Private(dataDir string) Core {
	dir := filepath.Join(dataDir, "core")
	return Core{
		Binary:    filepath.Join(dataDir, "libexec", "hc"),
		StateDir:  filepath.Join(dir, "state"),
		ConfigDir: filepath.Join(dir, "config"),
		Socket:    filepath.Join(dir, "run", "core.sock"),
	}
}

// Env returns base with every HC_* variable and the core's XDG paths replaced
// by this core's, for running c.Binary. A host started inside someone's own hc
// terminal would otherwise reach that core.
func (c Core) Env(base []string) []string {
	env := make([]string, 0, len(base)+5)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "HC_") || name == "XDG_STATE_HOME" || name == "XDG_CONFIG_HOME" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HC_SESSION="+Session,
		"HC_SOCKET="+c.Socket,
		"XDG_STATE_HOME="+c.StateDir,
		"XDG_CONFIG_HOME="+c.ConfigDir,
	)
}

// Start starts the core if it is not running, with this process's
// environment made private by Env. The core outlives the caller.
func (c Core) Start(ctx context.Context) error {
	return c.StartEnv(ctx, os.Environ())
}

// StartEnv is Start with base in place of this process's environment.
func (c Core) StartEnv(ctx context.Context, base []string) error {
	for _, dir := range []string{c.StateDir, c.ConfigDir, filepath.Dir(c.Socket)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		// MkdirAll leaves an existing directory's mode alone.
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	cmd := exec.CommandContext(ctx, c.Binary, "server", "start")
	cmd.Env = c.Env(base)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start herdr-core: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Dial connects to the running core and completes the handshake.
func (c Core) Dial(ctx context.Context) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, fmt.Errorf("connect to herdr-core: %w", err)
	}
	return NewClient(ctx, conn)
}
