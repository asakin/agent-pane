//go:build windows

package cmd

import "testing"

func TestCoreEnvUsesTheProfileForXDGDefaultsOnWindows(t *testing.T) {
	t.Setenv("HOME", "")
	env := coreEnv([]string{`USERPROFILE=C:\Users\me`, "HC_SOCKET=x", `XDG_STATE_HOME=D:\state`})
	if _, ok := env["HC_SOCKET"]; ok {
		t.Fatal("HC_SOCKET passed on")
	}
	if env["XDG_STATE_HOME"] != `D:\state` || env["XDG_CONFIG_HOME"] != `C:\Users\me\.config` {
		t.Fatalf("env %v", env)
	}

	t.Setenv("USERPROFILE", `C:\Users\host`)
	env = coreEnv(nil)
	if env["XDG_CONFIG_HOME"] != `C:\Users\host\.config` || env["XDG_STATE_HOME"] != `C:\Users\host\.local\state` {
		t.Fatalf("env with no session profile %v", env)
	}
}
