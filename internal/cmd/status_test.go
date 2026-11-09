package cmd

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStatusReport(t *testing.T) {
	for _, tc := range []struct {
		state, msg, kind string
		want             string
	}{
		{"working", "", "", "\x1b]7501;state=working\x1b\\"},
		{"done", "all good", "", "\x1b]7501;state=done:msg=" + base64.StdEncoding.EncodeToString([]byte("all good")) + "\x1b\\"},
		{"blocked", "allow?", "permission", "\x1b]7501;state=blocked:msg=" + base64.StdEncoding.EncodeToString([]byte("allow?")) + ":kind=permission\x1b\\"},
		{"working", "two\nlines\x1b]0;x", "", "\x1b]7501;state=working:msg=" + base64.StdEncoding.EncodeToString([]byte("two lines\x1b]0;x")) + "\x1b\\"},
		{"clear", "", "", "\x1b]7501;state=clear\x1b\\"},
	} {
		got, err := statusReport(tc.state, tc.msg, tc.kind)
		if err != nil || got != tc.want {
			t.Errorf("statusReport(%q, %q, %q) = %q, %v; want %q", tc.state, tc.msg, tc.kind, got, err, tc.want)
		}
	}
	for _, tc := range [][3]string{
		{"busy", "", ""},
		{"working", "", "permission"},
		{"blocked", "", "lunch"},
		{"clear", "why", ""},
	} {
		if _, err := statusReport(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("statusReport%q succeeded", tc)
		}
	}
}

func TestStatusMessageIsCutToWhatTheCoreAccepts(t *testing.T) {
	msg := trimStatusMsg(strings.Repeat("é", maxStatusMsg))
	if len(msg) > maxStatusMsg || !utf8.ValidString(msg) || !strings.HasSuffix(msg, "…") {
		t.Fatalf("trimmed to %d bytes, valid %v", len(msg), utf8.ValidString(msg))
	}
	if got := trimStatusMsg("  a\tb  "); got != "a b" {
		t.Fatalf("%q", got)
	}
}

func TestStatusNeedsAnAgentPaneTerminal(t *testing.T) {
	t.Setenv(envPresence, "")
	cmd := NewCmdStatus()
	cmd.SetArgs([]string{"working"})
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err == nil || err.Error() != errNotInTerminal {
		t.Fatalf("err %v", err)
	}
}
