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

func TestSetEnvFold(t *testing.T) {
	got := setEnvFold([]string{"A=1", "Path=old", "B=2", "PATH=dup"}, "PATH", "new")
	if want := "A=1 Path=new B=2"; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
	got = setEnvFold([]string{"A=1"}, "PATH", "new")
	if want := "A=1 PATH=new"; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestWindowsPATH(t *testing.T) {
	got := windowsPATH(`C:\Windows\system32;C:\Windows`, `C:\Users\me\AppData\Roaming\npm;;`, `c:\windows\System32\;D:\chrome;C:\Users\Me\AppData\Roaming\npm`)
	if want := `C:\Windows\system32;C:\Windows;C:\Users\me\AppData\Roaming\npm;D:\chrome`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := windowsPATH("", "", `C:\a`); got != `C:\a` {
		t.Errorf("host PATH only: got %q", got)
	}
}

func TestIsBatchFile(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\npm\claude.cmd`: true,
		`C:\npm\claude.CMD`: true,
		`C:\x\run.Bat`:      true,
		`C:\x\claude.exe`:   false,
		`C:\x.cmd\claude`:   false,
	} {
		if got := isBatchFile(path); got != want {
			t.Errorf("isBatchFile(%q) = %v", path, got)
		}
	}
}

func TestBatchCommandLine(t *testing.T) {
	const shell = `C:\Windows\System32\cmd.exe`
	got, err := batchCommandLine(shell, []string{`C:\Program Files\npm\claude.cmd`, "--model", "a b&c|d<e>f^g!h", "", `C:\dir\`})
	if err != nil {
		t.Fatal(err)
	}
	want := `"C:\Windows\System32\cmd.exe" /d /v:off /s /c ""C:\Program Files\npm\claude.cmd" "--model" "a b&c|d<e>f^g!h" "" "C:\dir\\""`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	for _, arg := range []string{`say "hi"`, "%PATH%", "a\nb", "a\rb", "a\x00b"} {
		if _, err := batchCommandLine(shell, []string{`C:\npm\claude.cmd`, arg}); err == nil {
			t.Errorf("argument %q accepted", arg)
		}
	}
	if _, err := batchCommandLine(`C:\%x%\cmd.exe`, []string{`C:\npm\claude.cmd`}); err == nil {
		t.Error("shell with a % accepted")
	}
}
