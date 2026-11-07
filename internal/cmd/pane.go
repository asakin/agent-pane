package cmd

import (
	"crypto/rand"
	"strings"
	"sync"
)

// paneTab is the host's record of one pane tab: the logical terminal tab in
// the side panel, as distinct from the PTY session running in it. Page-read
// grants belong here, so they survive a coding agent exiting and the panel
// replacing a dead PTY session in the same pane tab, and end when the pane
// tab closes. The id is made here and handed only to the extension; nothing
// running in the terminal sees it.
type paneTab struct {
	id string

	mu          sync.Mutex
	closed      bool
	allowAll    bool
	allowedTabs map[int]bool
	// turn holds one token: the right to show this pane tab's approval
	// prompt. A channel, so waiting for it can give up.
	turn chan struct{}
}

func newPaneTab() *paneTab {
	p := &paneTab{id: "pane-" + strings.ToLower(rand.Text()), turn: make(chan struct{}, 1)}
	p.turn <- struct{}{}
	return p
}

// isAllowed reports whether a grant already covers reads of tabID.
func (p *paneTab) isAllowed(tabID int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && (p.allowAll || p.allowedTabs[tabID])
}

// grant records an answer unless the pane tab has closed.
func (p *paneTab) grant(tabID int, choice string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	switch choice {
	case "tab":
		if p.allowedTabs == nil {
			p.allowedTabs = map[int]bool{}
		}
		p.allowedTabs[tabID] = true
	case "all":
		p.allowAll = true
	}
	return true
}

func (p *paneTab) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.allowAll = false
	p.allowedTabs = nil
}

// paneLocked returns the record for a new PTY session; the caller holds m.mu
// and registers the session before releasing it. A session started for an
// existing pane tab (the panel replacing a dead one) joins that pane tab's
// record, unless another live session already holds it. An id the host does
// not know, such as one from before the host restarted, gets a fresh record
// with no grants.
func (m *SessionManager) paneLocked(id string) *paneTab {
	if p, ok := m.panes[id]; ok && id != "" {
		held := false
		for _, s := range m.sessions {
			if s.pane == p && !s.isClosed() {
				held = true
				break
			}
		}
		if !held {
			return p
		}
	}
	p := newPaneTab()
	m.panes[p.id] = p
	return p
}

// forgetIdlePaneLocked drops a pane tab's record once no session runs in it
// and it holds no grant: a replacement would start with nothing either way.
// The caller holds m.mu.
func (m *SessionManager) forgetIdlePaneLocked(p *paneTab) {
	if p == nil {
		return
	}
	for _, s := range m.sessions {
		if s.pane == p {
			return
		}
	}
	p.mu.Lock()
	idle := !p.allowAll && len(p.allowedTabs) == 0
	p.mu.Unlock()
	if idle {
		delete(m.panes, p.id)
	}
}

// ClosePane ends a pane tab's grants. Reads still waiting under them fail
// when they next check, and a late approval cannot bring them back.
func (m *SessionManager) ClosePane(id string) {
	m.mu.Lock()
	p, ok := m.panes[id]
	delete(m.panes, id)
	m.mu.Unlock()
	if ok {
		p.close()
	}
}
