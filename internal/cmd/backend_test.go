package cmd

import (
	"errors"
	"sync"
	"testing"
)

// sessionOutput is the scrollback of a session running in a ptyTerminal.
func sessionOutput(s *Session) string {
	t := s.term.(*ptyTerminal)
	t.outMu.Lock()
	defer t.outMu.Unlock()
	return string(t.buf.Bytes())
}

type recordingViewer struct {
	mu     sync.Mutex
	frames []string
	closed bool
	fail   bool
}

func (v *recordingViewer) send(p []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.fail {
		return errors.New("viewer gone")
	}
	v.frames = append(v.frames, string(p))
	return nil
}

func (v *recordingViewer) close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	return nil
}

func TestPtyTerminalAttachSendsOneReplayFrame(t *testing.T) {
	term := newPtyTerminal(nil, 1024)
	first := &recordingViewer{}
	if _, err := term.attach(first); err != nil {
		t.Fatal(err)
	}
	if len(first.frames) != 1 || first.frames[0] != "" {
		t.Fatalf("empty terminal replay %q", first.frames)
	}

	_, _ = term.buf.Write([]byte("hello"))
	second := &recordingViewer{}
	detach, err := term.attach(second)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.frames) != 1 || second.frames[0] != "hello" {
		t.Fatalf("replay %q", second.frames)
	}
	if !first.closed {
		t.Fatal("a newer viewer did not replace the first")
	}
	detach()
	if term.viewer != nil {
		t.Fatal("detach kept the viewer")
	}
}

func TestPtyTerminalReplayRestoresModesAfterWrap(t *testing.T) {
	term := newPtyTerminal(nil, 8)
	for _, chunk := range []string{"\x1b[?1049h", "0123456789"} {
		_, _ = term.buf.Write([]byte(chunk))
		term.modes.Write([]byte(chunk))
	}
	v := &recordingViewer{}
	if _, err := term.attach(v); err != nil {
		t.Fatal(err)
	}
	prefix, suffix := term.modes.Restore()
	if want := prefix + "23456789" + suffix; v.frames[0] != want {
		t.Fatalf("replay %q, want %q", v.frames[0], want)
	}
}

func TestPtyTerminalAttachFailureKeepsCurrentViewer(t *testing.T) {
	term := newPtyTerminal(nil, 1024)
	current := &recordingViewer{}
	if _, err := term.attach(current); err != nil {
		t.Fatal(err)
	}
	if _, err := term.attach(&recordingViewer{fail: true}); err == nil {
		t.Fatal("attach to a failing viewer succeeded")
	}
	if term.viewer != current || current.closed {
		t.Fatal("a failed attach replaced the viewer")
	}
}
