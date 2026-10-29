package cmd

import (
	"strings"
	"testing"
)

func TestLoginShellArgs(t *testing.T) {
	cases := []struct {
		shell string
		args  []string
		want  string
	}{
		{"/bin/zsh", nil, "-l"},
		{"/bin/bash", []string{"--noprofile"}, "-l --noprofile"},
		{"/usr/local/bin/fish", []string{"-l"}, "-l"},
		{"/bin/zsh", []string{"--login"}, "--login"},
		{"/bin/dash", nil, ""},
		{"/usr/bin/python3", []string{"-q"}, "-q"},
	}
	for _, c := range cases {
		got := strings.Join(loginShellArgs(c.shell, c.args), " ")
		if got != c.want {
			t.Fatalf("%s %v: got %q, want %q", c.shell, c.args, got, c.want)
		}
	}
}

func TestMergePtyEnvReplacesPATH(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"PATH=/should/not/win", // duplicate — first wins until overlay replaces
	}
	got := mergePtyEnv(base, map[string]string{
		"PATH": "/shim:/usr/bin",
		"TERM": "xterm-256color",
	})
	var path string
	pathCount := 0
	for _, e := range got {
		if strings.HasPrefix(e, "PATH=") {
			pathCount++
			path = strings.TrimPrefix(e, "PATH=")
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected one PATH, got %d in %v", pathCount, got)
	}
	if path != "/shim:/usr/bin" {
		t.Fatalf("PATH not replaced: %q", path)
	}
}
