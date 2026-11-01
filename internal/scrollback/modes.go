package scrollback

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Modes tracks the DEC private modes a program turns on and off (CSI ? n h/l)
// so a replay that has lost its oldest bytes can put the terminal back in the
// same state. A fullscreen TUI sets alt screen and mouse tracking once at
// startup; without this, a reattach after the ring wraps lands in the normal
// buffer with mouse tracking off and the scroll wheel does nothing.
type Modes struct {
	mu       sync.Mutex
	state    int // parser: 0 ground, 1 ESC, 2 ESC [, 3 ESC [ ? params
	params   []byte
	alt      bool
	tracking int          // 0 or 9, 1000, 1002, 1003
	encoding int          // 0 or 1005, 1006, 1015, 1016
	flags    map[int]bool // other modes, by last value set
}

// Modes restored by their last value. Others are left to the replay.
var restoredFlags = map[int]bool{
	1:    true, // application cursor keys
	25:   true, // cursor visible
	1004: true, // focus events
	2004: true, // bracketed paste
}

func (m *Modes) Write(p []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range p {
		switch m.state {
		case 0:
			if b == 0x1b {
				m.state = 1
			}
		case 1:
			m.state = 0
			if b == '[' {
				m.state = 2
			} else if b == 0x1b {
				m.state = 1
			}
		case 2:
			m.state = 0
			if b == '?' {
				m.state = 3
				m.params = m.params[:0]
			} else if b == 0x1b {
				m.state = 1
			}
		case 3:
			switch {
			case b >= '0' && b <= '9' || b == ';':
				m.params = append(m.params, b)
			case b == 'h' || b == 'l':
				m.apply(string(m.params), b == 'h')
				m.state = 0
			case b == 0x1b:
				m.state = 1
			default:
				m.state = 0
			}
		}
	}
}

func (m *Modes) apply(params string, on bool) {
	for _, s := range strings.Split(params, ";") {
		n, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		switch n {
		case 47, 1047, 1049:
			m.alt = on
		case 9, 1000, 1002, 1003:
			if on {
				m.tracking = n
			} else {
				m.tracking = 0
			}
		case 1005, 1006, 1015, 1016:
			if on {
				m.encoding = n
			} else if m.encoding == n {
				m.encoding = 0
			}
		default:
			if restoredFlags[n] {
				if m.flags == nil {
					m.flags = map[int]bool{}
				}
				m.flags[n] = on
			}
		}
	}
}

// Restore returns what to send before a replay (alt screen, so the replayed
// redraws land in the right buffer) and after it (the current input modes).
func (m *Modes) Restore() (prefix, suffix string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alt {
		prefix = "\x1b[?1049h"
	}
	var b strings.Builder
	if m.tracking != 0 {
		fmt.Fprintf(&b, "\x1b[?%dh", m.tracking)
	}
	if m.encoding != 0 {
		fmt.Fprintf(&b, "\x1b[?%dh", m.encoding)
	}
	keys := make([]int, 0, len(m.flags))
	for n := range m.flags {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for _, n := range keys {
		c := byte('l')
		if m.flags[n] {
			c = 'h'
		}
		fmt.Fprintf(&b, "\x1b[?%d%c", n, c)
	}
	return prefix, b.String()
}
