package cmd

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
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
	// epoch counts revocations, so an answer to a prompt shown before one
	// cannot bring the grants back.
	epoch int
	// turn holds one token: the right to show this pane tab's approval
	// prompt. A channel, so waiting for it can give up.
	turn chan struct{}
}

func newPaneTab() *paneTab {
	return newPaneTabWithID("pane-" + strings.ToLower(rand.Text()))
}

func newPaneTabWithID(id string) *paneTab {
	p := &paneTab{id: id, turn: make(chan struct{}, 1)}
	p.turn <- struct{}{}
	return p
}

// paneIDRe matches the ids newPaneTab makes.
var paneIDRe = regexp.MustCompile(`^pane-[a-z2-7]{26}$`)

// publicID names a host id to programs in the terminal (AGENT_PANE_TAB_ID,
// AGENT_PANE_SESSION_ID) without handing them the id itself.
func publicID(prefix, id string) string {
	sum := sha256.Sum256([]byte("agent-pane:" + prefix + ":" + id))
	return prefix + "-" + hex.EncodeToString(sum[:16])
}

// isAllowed reports whether a grant already covers reads of tabID.
func (p *paneTab) isAllowed(tabID int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.closed && (p.allowAll || p.allowedTabs[tabID])
}

// grant records an answer unless the pane tab has closed.
func (p *paneTab) grant(tabID int, choice string) bool {
	return p.grantAt(p.currentEpoch(), tabID, choice) == nil
}

// currentEpoch is the revocation count to pass to grantAt for a prompt
// shown now.
func (p *paneTab) currentEpoch() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.epoch
}

// errGrantRevoked refuses an answer to a prompt shown before the pane tab's
// grants were revoked.
var errGrantRevoked = errors.New("the pane tab's approvals ended while the prompt was open")

// errPaneClosed refuses an answer once the pane tab has closed.
var errPaneClosed = errors.New("the pane tab has closed")

// grantAt records an answer to a prompt shown at epoch, unless the pane tab
// has closed or its grants were revoked since.
func (p *paneTab) grantAt(epoch, tabID int, choice string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errPaneClosed
	}
	if p.epoch != epoch {
		return errGrantRevoked
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
	return nil
}

// revoke drops the pane tab's grants and leaves it open for new ones, as
// when the Chrome window it was shown in closes and the pane tab lives on.
// A prompt already open cannot grant any more.
func (p *paneTab) revoke() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.epoch++
	p.allowAll = false
	p.allowedTabs = nil
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
// with no grants. It keeps that id when it is one the host could have made
// and is free, so the pane tab's identity (AGENT_PANE_TAB_ID) outlives the
// record being forgotten or the host restarting.
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
	var p *paneTab
	if _, known := m.panes[id]; !known && paneIDRe.MatchString(id) {
		p = newPaneTabWithID(id)
	} else {
		p = newPaneTab()
	}
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
