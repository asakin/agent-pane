//go:build unix

package herdrcore

import (
	"slices"
	"strings"
	"testing"
)

func TestPrivatePaths(t *testing.T) {
	c := Private("/home/u/.local/share/agent-pane")
	want := Core{
		Binary:    "/home/u/.local/share/agent-pane/libexec/hc",
		StateDir:  "/home/u/.local/share/agent-pane/core/state",
		ConfigDir: "/home/u/.local/share/agent-pane/core/config",
		RunDir:    "/home/u/.local/share/agent-pane/core/run",
		Socket:    "/home/u/.local/share/agent-pane/core/run/core.sock",
	}
	if c != want {
		t.Fatalf("Private = %+v, want %+v", c, want)
	}
}

func TestEnvReplacesTheUsersCore(t *testing.T) {
	c := Private("/data")
	got := c.Env([]string{
		"HOME=/home/u",
		"PATH=/usr/bin",
		// Inside a person's own hc terminal these name their core.
		"HC_SESSION=default",
		"HC_SOCKET=/run/user/1000/herdr-core/default.sock",
		"HC_TERMINAL_ID=t3",
		"HC_TERMINAL_SOCKET=/run/user/1000/herdr-core/default.holder.sock",
		"XDG_STATE_HOME=/home/u/.local/state",
		"XDG_CONFIG_HOME=/home/u/.config",
	})
	want := []string{
		"HOME=/home/u",
		"PATH=/usr/bin",
		"HC_SESSION=agent-pane",
		"HC_SOCKET=/data/core/run/core.sock",
		"XDG_STATE_HOME=/data/core/state",
		"XDG_CONFIG_HOME=/data/core/config",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Env =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
