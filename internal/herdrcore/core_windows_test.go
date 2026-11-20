//go:build windows

package herdrcore

import (
	"slices"
	"strings"
	"testing"
)

func TestPrivatePathsOnWindows(t *testing.T) {
	t.Setenv("USERNAME", "Ada L.")
	data := `C:\Users\ada\AppData\Local\agent-pane`
	c := Private(data)
	if c.Binary != data+`\libexec\hc.exe` {
		t.Fatalf("Binary = %q", c.Binary)
	}
	if c.StateDir != data+`\core\state` || c.ConfigDir != data+`\core\config` || c.RunDir != data+`\core\run` {
		t.Fatalf("Private = %+v", c)
	}
	// A named pipe, not a file: herdr-core listens on a pipe on Windows.
	if !strings.HasPrefix(c.Socket, `\\.\pipe\agent-pane-hc-AdaL-`) || !strings.HasSuffix(c.Socket, ".sock") {
		t.Fatalf("Socket = %q", c.Socket)
	}
	// Windows paths ignore case: one install, one pipe.
	if again := Private(strings.ToUpper(data) + `\`); again.Socket != c.Socket {
		t.Fatalf("the same install got pipes %q and %q", c.Socket, again.Socket)
	}
	if other := Private(`C:\Users\ada\AppData\Local\other`); other.Socket == c.Socket {
		t.Fatalf("two installs share the pipe %q", c.Socket)
	}
}

func TestEnvNamesThePipeOnWindows(t *testing.T) {
	c := Private(`C:\data`)
	got := c.Env([]string{"USERPROFILE=C:\\Users\\u", "HC_SOCKET=\\\\.\\pipe\\herdr-core-u-default.sock"})
	want := []string{
		`USERPROFILE=C:\Users\u`,
		"HC_SESSION=agent-pane",
		"HC_SOCKET=" + c.Socket,
		`XDG_STATE_HOME=C:\data\core\state`,
		`XDG_CONFIG_HOME=C:\data\core\config`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Env =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
