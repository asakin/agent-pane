package cmd

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"agent-pane/internal/herdrcore"
)

// Durable (preview) pane tabs outlive the host that started them: their
// programs run in the core. A later host takes them back here, from the core
// terminals carrying Agent Pane's labels and the host-private registry that
// joins those public labels to the session and pane tab ids the extension
// holds. Grants are never carried over: a pane tab a host takes back starts
// with none, and the person approves reads again.

// SetBrowser records the extension's browser id (from initialize). The
// host takes back only the Durable pane tabs this browser started, and
// those no browser claims.
func (m *SessionManager) SetBrowser(id string) {
	m.mu.Lock()
	m.browser = id
	m.browserSet = true
	m.mu.Unlock()
}

// wantsRediscovery reports whether a core may hold pane tabs to take back:
// Durable is the setting, or the registry names a pane tab. Until initialize
// names the browser, which pane tabs are this one's is not known, so not yet.
func (m *SessionManager) wantsRediscovery() bool {
	if _, ok := m.durable.(*coreBackend); !ok || m.registry == nil {
		return false
	}
	m.mu.RLock()
	known := m.browserSet
	m.mu.RUnlock()
	if !known {
		return false
	}
	if cfg().String(terminalBackendKey) == terminalBackendDurable {
		return true
	}
	records, err := m.registry.load()
	return err == nil && len(records) > 0
}

// rediscoverRetry is how long after a failed attempt to take pane tabs back
// the host tries again, so an unreachable core does not slow every tty.list.
var rediscoverRetry = 10 * time.Second

// ensureRediscovered takes back, once per host, the Durable pane tabs still
// running in the core. A failed attempt (core unreachable) is tried again on
// a later call.
func (m *SessionManager) ensureRediscovered() {
	m.rediscoverMu.Lock()
	defer m.rediscoverMu.Unlock()
	if m.rediscovered || time.Since(m.lastRediscover) < rediscoverRetry || !m.wantsRediscovery() {
		return
	}
	m.lastRediscover = time.Now()
	if err := m.rediscover(); err != nil {
		m.logger.Warn("could not look for durable pane tabs", "error", err)
		return
	}
	m.rediscovered = true
}

func (m *SessionManager) rediscover() error {
	core := m.durable.(*coreBackend)
	ctx, cancel := context.WithTimeout(context.Background(), coreStartTimeout)
	defer cancel()
	conn, err := core.connect(ctx)
	if err != nil {
		return err
	}
	terms, err := conn.List(ctx, nil, true)
	if err != nil {
		return err
	}
	records, err := m.registry.load()
	if err != nil {
		m.logger.Warn("unreadable pane registry; running pane tabs are adopted without it", "error", err)
		records = map[string]paneRecord{}
	}
	bySession := map[string]paneRecord{}
	for _, rec := range records {
		bySession[rec.Session] = rec
	}

	m.mu.RLock()
	browser := m.browser
	held := map[string]bool{}
	for _, s := range m.sessions {
		held[publicID("session", s.ID)] = true
	}
	m.mu.RUnlock()
	ours := func(rec paneRecord) bool { return rec.Browser == "" || rec.Browser == browser }

	live := map[string]bool{}
	for _, term := range terms {
		label := term.Labels[coreSessionLabel]
		if label == "" || held[label] {
			continue
		}
		rec, known := bySession[label]
		if known && !ours(rec) {
			continue
		}
		if term.State != "live" {
			// Its program ended while no host watched it.
			if err := conn.Remove(ctx, term.Address); err != nil && !herdrcore.IsKind(err, "not_found") {
				m.logger.Debug("failed to remove finished core terminal", "error", err, "terminal", term.Address)
			}
			continue
		}
		live[label] = true
		if !known {
			rec = paneRecord{
				ID:      strings.ToLower(rand.Text()),
				Pane:    newPaneTab().id,
				Session: label,
				Agent:   term.Labels[coreAgentLabel],
				Orphan:  true,
			}
		}
		if rec.Browser == "" {
			rec.Browser = browser
		}
		if err := m.adopt(core, rec, term); err != nil {
			m.logger.Warn("failed to take back durable pane tab", "error", err, "terminal", term.Address)
			continue
		}
		if !known || records[rec.ID] != rec {
			if err := m.registry.put(rec); err != nil {
				m.logger.Warn("failed to record durable pane tab", "error", err)
			}
		}
	}
	// A record whose terminal is gone names nothing to take back. Sessions
	// started meanwhile are recorded too, so they are read again here.
	return m.registry.update(func(records map[string]paneRecord) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		for id, rec := range records {
			if _, running := m.sessions[id]; ours(rec) && !live[rec.Session] && !running {
				delete(records, id)
			}
		}
	})
}

// adopt makes rec's session again, running in the core terminal term.
func (m *SessionManager) adopt(core *coreBackend, rec paneRecord, term herdrcore.Terminal) error {
	t, err := core.adopt(rec.ID, term)
	if err != nil {
		return err
	}
	label := rec.Label
	if label == "" {
		label = "Terminal"
		if agent, ok := agentByID(rec.Agent); ok && rec.Agent != "" {
			label = agent.Name
		}
	}
	session := &Session{
		ID:      rec.ID,
		manager: m,
		Label:   label,
		term:    t,
		started: true,
		agent:   rec.Agent,
		window:  term.Labels[coreWindowLabel],
		// An orphan's programs were given a socket no record names.
		socketPath: rec.Socket,
	}
	m.mu.Lock()
	if _, taken := m.sessions[rec.ID]; taken || m.closing {
		m.mu.Unlock()
		t.detach()
		return nil
	}
	pane, ok := m.panes[rec.Pane]
	if !ok {
		pane = newPaneTabWithID(rec.Pane)
		m.panes[pane.id] = pane
	}
	session.pane = pane
	m.sessions[rec.ID] = session
	m.mu.Unlock()

	session.term.run(func() { m.reap(rec.ID) }, func(f terminalFacts) { m.reportFacts(session, f) })
	if rec.Socket != "" && term.Pid != nil && *term.Pid > 0 {
		go session.servePageSocketAt(rec.Socket, *term.Pid, m.extensionCaller)
	}
	return nil
}

// record keeps a Durable session in the registry, so a later host can take
// it back.
func (m *SessionManager) record(session *Session) {
	if m.registry == nil {
		return
	}
	m.mu.RLock()
	browser := m.browser
	m.mu.RUnlock()
	session.mu.Lock()
	rec := paneRecord{
		ID:      session.ID,
		Session: publicID("session", session.ID),
		Agent:   session.agent,
		Label:   session.Label,
		Socket:  session.socketPath,
		Browser: browser,
	}
	if session.pane != nil {
		rec.Pane = session.pane.id
	}
	session.mu.Unlock()
	if err := m.registry.put(rec); err != nil {
		m.logger.Warn("failed to record durable pane tab", "error", err)
	}
}

// release lets the host go of a Durable session and leaves its program
// running in the core, for a later host to take back.
func (m *SessionManager) release(id string) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		m.forgetIdlePaneLocked(session.pane)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	session.mu.Lock()
	session.detached = true
	session.mu.Unlock()
	m.dropTokens(id)
	session.finalize()
}

// DetachWindow is what closing the Chrome window that showed a pane tab
// does. A Durable session keeps running, unlabelled from that window, and
// its pane tab's grants end: they were given for tabs of a window that is
// gone. A Built-in session ends with its pane tab, as tty.destroy does. It
// reports whether the session lives on.
func (m *SessionManager) DetachWindow(id, pane string) bool {
	session, ok := m.Get(id)
	if !ok || !session.isDurable() {
		if pane != "" {
			m.ClosePane(pane)
		}
		_ = m.Destroy(id)
		return false
	}
	session.paneTab().revoke()
	if pane != "" {
		m.mu.RLock()
		p := m.panes[pane]
		m.mu.RUnlock()
		if p != nil {
			p.revoke()
		}
	}
	m.setWindow(session, "")
	return true
}

// setWindow records the Chrome window session is shown in, on its core
// terminal too for a Durable one, so a panel can find it after a restart.
func (m *SessionManager) setWindow(session *Session, window string) {
	session.mu.Lock()
	changed := session.window != window
	session.window = window
	term := session.term
	session.mu.Unlock()
	t, ok := term.(*coreTerminal)
	if !changed || !ok {
		return
	}
	if err := t.label(map[string]string{coreWindowLabel: window}); err != nil {
		m.logger.Debug("failed to label core terminal with its window", "error", err, "session", session.ID)
	}
}

func (s *Session) isDurable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.term.(*coreTerminal)
	return ok
}
