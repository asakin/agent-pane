package scrollback

import (
	"strings"
	"testing"
)

func TestModesRestoreAfterWrap(t *testing.T) {
	// A fullscreen TUI (Claude Code with "tui": "fullscreen") turns these on
	// once at startup and then only redraws.
	startup := "\x1b[?1049h\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?2004h\x1b[?1004h"
	r := New(64)
	m := &Modes{}
	for _, chunk := range []string{startup, strings.Repeat("\x1b[Hredraw", 20)} {
		r.Write([]byte(chunk))
		m.Write([]byte(chunk))
	}
	if !r.Dropped() {
		t.Fatal("ring should have dropped its oldest bytes")
	}

	prefix, suffix := m.Restore()
	if prefix != "\x1b[?1049h" {
		t.Fatalf("prefix = %q, want alt screen on", prefix)
	}
	for _, want := range []string{"\x1b[?1003h", "\x1b[?1006h", "\x1b[?2004h", "\x1b[?1004h"} {
		if !strings.Contains(suffix, want) {
			t.Fatalf("suffix %q is missing %q", suffix, want)
		}
	}
}

func TestModesFollowLatestState(t *testing.T) {
	m := &Modes{}
	m.Write([]byte("\x1b[?1049h\x1b[?1003h\x1b[?10"))
	m.Write([]byte("06h")) // sequence split across reads
	m.Write([]byte("\x1b[?1003l\x1b[?1049l"))

	prefix, suffix := m.Restore()
	if prefix != "" {
		t.Fatalf("prefix = %q, want none after leaving alt screen", prefix)
	}
	if strings.Contains(suffix, "\x1b[?1003h") || strings.Contains(suffix, "\x1b[?1000h") {
		t.Fatalf("suffix %q re-enables mouse tracking that was turned off", suffix)
	}
	if !strings.Contains(suffix, "\x1b[?1006h") {
		t.Fatalf("suffix %q is missing SGR encoding", suffix)
	}
}

func TestRingDropped(t *testing.T) {
	r := New(4)
	r.Write([]byte("abcd"))
	if r.Dropped() {
		t.Fatal("exactly full is not dropped")
	}
	r.Write([]byte("e"))
	if !r.Dropped() {
		t.Fatal("overflow should be dropped")
	}
}
