package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"agent-pane/internal/herdrcore"
)

// The layout document (RFC 0004) records how Durable pane tabs are arranged:
// one space per Chrome window, one tab per pane tab, in order, with names.
// It lives in the private core as layoutDocName, in the Herdr app's layout
// shape with Agent Pane's own fields under agentPane keys, so a side panel
// can restore whole spaces after Chrome quits, crashes or is killed, and any
// client of the core can read the arrangement.
//
// The host is its only writer. Every browser's host writes the same
// document, each its own spaces, with versioned doc.set: a write that loses
// the race reads the document again and merges once more. What the host reads
// is untrusted: ids, sockets and grants come from panes.json and the host's
// own sessions, never from the document, and the host's spaces are kept from
// its own model, so another client's change to their order or names is
// replaced by the next write.
const layoutDocName = "agent-pane"

// layoutWriteAttempts bounds how often one write retries a lost race.
const layoutWriteAttempts = 8

type layoutDoc struct {
	Spaces []layoutSpace `json:"spaces"`
}

type layoutSpace struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Folder    string          `json:"folder"`
	Tabs      []layoutTab     `json:"tabs"`
	AgentPane layoutSpaceMeta `json:"agentPane"`
}

type layoutSpaceMeta struct {
	// Window is the agent-pane.window value (<browserId>:<windowId>@<runId>)
	// of the Chrome window the space was last shown in. It stays after that
	// window closes, which is how a panel knows the space is shown nowhere.
	Window string `json:"window,omitempty"`
	// Active is the id of the tab shown last.
	Active string `json:"active,omitempty"`
}

type layoutTab struct {
	// ID is the pane tab's public id, its agent-pane.pane label.
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Root      layoutBlock   `json:"root"`
	AgentPane layoutTabMeta `json:"agentPane"`
}

// layoutBlock is a tab's whole tree: a pane tab holds one terminal.
type layoutBlock struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Content layoutBlockContent `json:"content"`
}

type layoutBlockContent struct {
	Terminal string `json:"terminal"`
}

type layoutTabMeta struct {
	Agent string `json:"agent,omitempty"`
	// LockedTab is the Chrome tab the pane tab reads pages from, an id that
	// means something only in the Chrome run its space's window names.
	LockedTab *int `json:"lockedTab,omitempty"`
}

// layoutIDRe matches the space and tab ids the host takes from a document.
var layoutIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// parseLayout reads a document's value, keeping only well-formed spaces and
// tabs. An unreadable value is an empty layout: the next write replaces it.
func parseLayout(raw json.RawMessage) layoutDoc {
	var doc layoutDoc
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return layoutDoc{}
	}
	seen := map[string]bool{}
	spaces := doc.Spaces[:0]
	for _, sp := range doc.Spaces {
		if !layoutIDRe.MatchString(sp.ID) || seen["space:"+sp.ID] {
			continue
		}
		seen["space:"+sp.ID] = true
		tabs := sp.Tabs[:0]
		for _, tab := range sp.Tabs {
			if !layoutIDRe.MatchString(tab.ID) || seen[tab.ID] {
				continue
			}
			seen[tab.ID] = true
			tabs = append(tabs, tab.normal())
		}
		sp.Tabs = tabs
		spaces = append(spaces, sp)
	}
	doc.Spaces = spaces
	return doc
}

// normal is the tab with its root the single block a pane tab has.
func (tab layoutTab) normal() layoutTab {
	tab.Root = layoutBlock{ID: tab.ID, Type: "block", Content: layoutBlockContent{Terminal: tab.Root.Content.Terminal}}
	return tab
}

func newLayoutID(prefix string) string {
	return prefix + "-" + strings.ToLower(rand.Text())[:16]
}

// layoutPlace is where tty.list says a Durable pane tab sits in the layout.
type layoutPlace struct {
	// Space is the space's id; Window the Chrome window it was last shown
	// in, as the extension names it (windowName), when that was this
	// browser's.
	Space    string `json:"id"`
	Name     string `json:"name,omitempty"`
	Window   string `json:"window,omitempty"`
	Position int    `json:"position"`
	Active   bool   `json:"active,omitempty"`
	// LockedTab is the Chrome tab the pane tab was locked to.
	LockedTab *int `json:"lockedTab,omitempty"`
}

// layoutTabReport is one pane tab of a side panel's tty.layout report.
type layoutTabReport struct {
	ID        string `json:"id"`
	LockedTab *int   `json:"lockedTab,omitempty"`
}

// layoutReport is tty.layout's params: the pane tabs a window's side panel
// shows, in order, the active one, and, when the panel took a space shown
// nowhere, that space's id.
type layoutReport struct {
	Window string            `json:"window"`
	Tabs   []layoutTabReport `json:"tabs"`
	Active string            `json:"active"`
	Space  string            `json:"space"`
}

// layoutSession is what the host knows of one of its Durable sessions.
type layoutSession struct {
	session  *Session
	tab      string
	name     string
	agent    string
	window   string
	terminal string
}

// layoutSessions are the host's live Durable sessions by public pane id.
func (m *SessionManager) layoutSessions() map[string]layoutSession {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.RUnlock()
	out := map[string]layoutSession{}
	for _, s := range sessions {
		s.mu.Lock()
		t, durable := s.term.(*coreTerminal)
		if !durable || !s.started || s.closed || s.pane == nil {
			s.mu.Unlock()
			continue
		}
		ls := layoutSession{session: s, tab: publicID("tab", s.pane.id), name: s.Label, agent: s.agent, window: s.window}
		s.mu.Unlock()
		t.mu.Lock()
		ls.terminal = t.address
		t.mu.Unlock()
		out[ls.tab] = ls
	}
	return out
}

// syncLayout applies change to the host's spaces and writes the document. A
// failure is logged: the next write tries again with everything since.
func (m *SessionManager) syncLayout(change *layoutChange) {
	if err := m.writeLayout(change); err != nil {
		m.logger.Warn("failed to write the pane tab layout", "error", err)
	}
}

// writeLayout applies change to the host's spaces and writes the document,
// once the host knows every Durable pane tab it holds. Before that, or while
// the host shuts down, it writes nothing: tabs not taken back yet would be
// dropped.
func (m *SessionManager) writeLayout(change *layoutChange) error {
	if _, ok := m.durable.(*coreBackend); !ok {
		return nil
	}
	if !m.ensureRediscovered() {
		return nil
	}
	return m.writeLayoutNow(change)
}

// layoutPendingMax bounds the windows whose reports wait for a document not
// read yet.
const layoutPendingMax = 256

// queueLayoutChange keeps a report until the document is read: each window's
// newest, which replaces its older one but keeps the space that one took, in
// the order the newest arrived. Each report is a window's whole arrangement,
// so the newest of each, in that order, ends where all of them would.
// Guarded by layoutMu.
func (m *SessionManager) queueLayoutChange(c layoutChange) error {
	if i := slicesIndex(m.layoutPending, func(p layoutChange) bool { return p.window == c.window }); i >= 0 {
		if c.space == "" {
			c.space = m.layoutPending[i].space
		}
		m.layoutPending = slices.Delete(m.layoutPending, i, i+1)
	} else if len(m.layoutPending) >= layoutPendingMax {
		return fmt.Errorf("too many windows report a layout before the document is read")
	}
	m.layoutPending = append(m.layoutPending, c)
	return nil
}

// writeLayoutNow applies change to the host's model, then writes the
// document. The model changes first, so a write that fails loses nothing:
// the next one carries it. One write runs at a time; a caller that finds one
// running leaves its change for that write to carry and returns at once, so
// no request waits behind another's document I/O.
func (m *SessionManager) writeLayoutNow(change *layoutChange) error {
	core, ok := m.durable.(*coreBackend)
	if !ok {
		return nil
	}
	m.mu.RLock()
	closing, browser := m.closing, m.browser
	m.mu.RUnlock()
	if closing {
		return nil
	}
	live := m.layoutSessions()
	m.layoutMu.Lock()
	if !m.layoutLoaded && len(live) == 0 {
		// Nothing to record, and the core need not start for it.
		m.layoutMu.Unlock()
		return nil
	}
	if change != nil {
		if m.layoutLoaded {
			m.layout = change.apply(m.layout)
		} else if err := m.queueLayoutChange(*change); err != nil {
			// The host's spaces come from the document, read first.
			m.layoutMu.Unlock()
			return err
		}
	}
	m.layoutDirty = true
	if m.layoutWriting {
		m.layoutMu.Unlock()
		return nil
	}
	m.layoutWriting = true
	m.layoutMu.Unlock()
	for {
		err := m.writeLayoutOnce(core, browser)
		m.layoutMu.Lock()
		if err != nil {
			m.layoutDirty = true
		}
		if err != nil || !m.layoutDirty {
			m.layoutWriting = false
			m.layoutMu.Unlock()
			return err
		}
		m.layoutMu.Unlock()
	}
}

// writeLayoutOnce writes the host's model into the document as it is now.
func (m *SessionManager) writeLayoutOnce(core *coreBackend, browser string) error {
	ctx, cancel := context.WithTimeout(context.Background(), coreCallTimeout)
	defer cancel()
	conn, err := core.connect(ctx)
	if err != nil {
		return err
	}
	for range layoutWriteAttempts {
		m.layoutMu.Lock()
		m.layoutDirty = false
		m.layoutMu.Unlock()
		doc, err := conn.DocGet(ctx, layoutDocName)
		if err != nil {
			return err
		}
		// Read after the document: a host records a pane tab before it
		// writes its tab, so every tab read here has its record.
		recorded, err := m.recordedTabs()
		if err != nil {
			// Without the records, other hosts' tabs cannot be told from
			// ended ones: write nothing rather than drop them.
			return err
		}
		read := parseLayout(doc.Value)
		m.layoutMu.Lock()
		// Taken while the model cannot change, so every pane tab a change in
		// the model names is in it: a session that starts later is written
		// by the next pass.
		live := m.layoutSessions()
		if !m.layoutLoaded {
			m.layout = ownSpaces(read, browser, live)
			for _, change := range m.layoutPending {
				m.layout = change.apply(m.layout)
			}
			m.layoutPending = nil
			m.layoutLoaded = true
		}
		m.layout = reconcileSpaces(m.layout, live)
		next := mergeLayout(read, m.layout, live, recorded)
		m.layoutMu.Unlock()
		if sameLayout(doc.Value, next) {
			return nil
		}
		_, err = conn.DocSet(ctx, layoutDocName, next, doc.Version)
		if herdrcore.IsKind(err, "conflict") {
			continue
		}
		return err
	}
	return errors.New("the pane tab layout kept changing while it was written")
}

// recordedTabs are the public pane ids panes.json names, any browser's.
func (m *SessionManager) recordedTabs() (map[string]bool, error) {
	records, err := m.registry.load()
	if err != nil {
		return nil, fmt.Errorf("failed to read the pane tab records: %w", err)
	}
	recorded := map[string]bool{}
	for _, rec := range records {
		recorded[publicID("tab", rec.Pane)] = true
	}
	return recorded, nil
}

// ownSpaces picks the host's spaces from a document it read at start: those
// last shown in a window of its browser, and those holding its pane tabs.
func ownSpaces(read layoutDoc, browser string, live map[string]layoutSession) []layoutSpace {
	var own []layoutSpace
	for _, sp := range read.Spaces {
		mine := browser != "" && strings.HasPrefix(sp.AgentPane.Window, browser+":")
		for _, tab := range sp.Tabs {
			if _, ok := live[tab.ID]; ok {
				mine = true
			}
		}
		if mine {
			own = append(own, sp)
		}
	}
	return own
}

// reconcileSpaces makes the host's spaces name exactly its live Durable pane
// tabs, each once, with the names and terminals the host knows. A pane tab
// with no tab is added at the end of the space for its window.
func reconcileSpaces(spaces []layoutSpace, live map[string]layoutSession) []layoutSpace {
	placed := map[string]bool{}
	out := make([]layoutSpace, 0, len(spaces))
	for _, sp := range spaces {
		tabs := make([]layoutTab, 0, len(sp.Tabs))
		for _, tab := range sp.Tabs {
			ls, ok := live[tab.ID]
			if !ok || placed[tab.ID] {
				continue
			}
			placed[tab.ID] = true
			tabs = append(tabs, ls.layoutTab(tab.AgentPane.LockedTab))
		}
		sp.Tabs = tabs
		out = append(out, sp)
	}
	for _, ls := range sortedSessions(live) {
		if placed[ls.tab] {
			continue
		}
		i := slicesIndex(out, func(sp layoutSpace) bool { return sp.AgentPane.Window == ls.window })
		if i < 0 {
			out = append(out, layoutSpace{ID: newLayoutID("sp"), AgentPane: layoutSpaceMeta{Window: ls.window}})
			i = len(out) - 1
		}
		out[i].Tabs = append(out[i].Tabs, ls.layoutTab(nil))
	}
	kept := out[:0]
	for _, sp := range out {
		if len(sp.Tabs) == 0 {
			continue
		}
		if slicesIndex(sp.Tabs, func(t layoutTab) bool { return t.ID == sp.AgentPane.Active }) < 0 {
			sp.AgentPane.Active = sp.Tabs[0].ID
		}
		if sp.Tabs == nil {
			sp.Tabs = []layoutTab{}
		}
		kept = append(kept, sp)
	}
	return kept
}

func (ls layoutSession) layoutTab(locked *int) layoutTab {
	return layoutTab{
		ID:        ls.tab,
		Name:      ls.name,
		Root:      layoutBlock{ID: ls.tab, Type: "block", Content: layoutBlockContent{Terminal: ls.terminal}},
		AgentPane: layoutTabMeta{Agent: ls.agent, LockedTab: locked},
	}
}

// sortedSessions orders sessions by pane id, so tabs added back come in a
// stable order.
func sortedSessions(live map[string]layoutSession) []layoutSession {
	out := make([]layoutSession, 0, len(live))
	for _, ls := range live {
		out = append(out, ls)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].tab < out[j-1].tab; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func slicesIndex[T any](s []T, f func(T) bool) int {
	for i, v := range s {
		if f(v) {
			return i
		}
	}
	return -1
}

// mergeLayout is the document to write: the spaces of other hosts as read,
// without tabs no record names or that are this host's, and this host's
// spaces from its own model, where the document had them. Another host's
// tabs in one of this host's spaces stay, after this host's own.
func mergeLayout(read layoutDoc, own []layoutSpace, live map[string]layoutSession, recorded map[string]bool) layoutDoc {
	ownByID := map[string]int{}
	for i, sp := range own {
		ownByID[sp.ID] = i
	}
	used := map[int]bool{}
	next := layoutDoc{Spaces: []layoutSpace{}}
	for _, sp := range read.Spaces {
		tabs := []layoutTab{}
		for _, tab := range sp.Tabs {
			if _, mine := live[tab.ID]; mine || !recorded[tab.ID] {
				continue
			}
			tabs = append(tabs, tab)
		}
		if i, ok := ownByID[sp.ID]; ok {
			mine := own[i]
			mine.Tabs = append(slices.Clip(mine.Tabs), tabs...)
			next.Spaces = append(next.Spaces, mine)
			used[i] = true
			continue
		}
		if len(tabs) == 0 {
			continue
		}
		sp.Tabs = tabs
		next.Spaces = append(next.Spaces, sp)
	}
	for i, sp := range own {
		if !used[i] {
			next.Spaces = append(next.Spaces, sp)
		}
	}
	return next
}

func sameLayout(raw json.RawMessage, doc layoutDoc) bool {
	data, err := json.Marshal(doc)
	if err != nil {
		return false
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(data, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// applyLayoutReport is tty.layout: the window's space becomes the pane tabs
// its side panel reports, in that order, with that one active and those
// locks. With a space id, that space is the one the window took.
func (m *SessionManager) applyLayoutReport(r layoutReport) error {
	if !validWindow(r.Window) || r.Window == "" {
		return fmt.Errorf("invalid window %q", r.Window)
	}
	window := m.windowLabel(r.Window)
	change := layoutChange{window: window, space: r.Space, tabs: []layoutTab{}}
	for _, rt := range r.Tabs {
		s, ok := m.Get(rt.ID)
		if !ok || !s.isDurable() {
			// Built-in pane tabs are not in the layout.
			continue
		}
		s.mu.Lock()
		pane := s.pane
		s.mu.Unlock()
		if pane == nil {
			continue
		}
		tab := publicID("tab", pane.id)
		change.tabs = append(change.tabs, layoutTab{ID: tab, AgentPane: layoutTabMeta{LockedTab: rt.LockedTab}})
		if rt.ID == r.Active {
			change.active = tab
		}
		m.setWindow(s, window)
	}
	return m.writeLayout(&change)
}

// layoutChange is one tty.layout report as the host applies it: the window's
// pane tabs by public id, in order, with their locks, the active one, and
// the space the window took, if it took one.
type layoutChange struct {
	window string
	space  string
	tabs   []layoutTab
	active string
}

// apply makes the change's window show its tabs: they leave the spaces they
// were in, and another space the window held is left to be offered.
func (c layoutChange) apply(spaces []layoutSpace) []layoutSpace {
	target := -1
	if c.space != "" {
		target = slicesIndex(spaces, func(sp layoutSpace) bool { return sp.ID == c.space })
	}
	if target < 0 {
		target = slicesIndex(spaces, func(sp layoutSpace) bool { return sp.AgentPane.Window == c.window })
	}
	if target < 0 {
		if len(c.tabs) == 0 {
			return spaces
		}
		spaces = append(spaces, layoutSpace{ID: newLayoutID("sp")})
		target = len(spaces) - 1
	}
	listed := map[string]bool{}
	for _, t := range c.tabs {
		listed[t.ID] = true
	}
	for i := range spaces {
		kept := []layoutTab{}
		for _, tab := range spaces[i].Tabs {
			if !listed[tab.ID] {
				kept = append(kept, tab)
			}
		}
		spaces[i].Tabs = kept
		if i != target && spaces[i].AgentPane.Window == c.window {
			spaces[i].AgentPane.Window = ""
		}
	}
	// Tabs the space had that the panel does not show stay after its own.
	spaces[target].Tabs = append(slices.Clone(c.tabs), spaces[target].Tabs...)
	spaces[target].AgentPane.Window = c.window
	if c.active != "" {
		spaces[target].AgentPane.Active = c.active
	}
	return spaces
}

// layoutPlaces is where each of the host's Durable pane tabs sits, by public
// pane id, for tty.list.
func (m *SessionManager) layoutPlaces() map[string]layoutPlace {
	m.layoutMu.Lock()
	defer m.layoutMu.Unlock()
	m.mu.RLock()
	browser := m.browser
	m.mu.RUnlock()
	places := map[string]layoutPlace{}
	for _, sp := range m.layout {
		window := ""
		if b, w, ok := strings.Cut(sp.AgentPane.Window, ":"); ok && b == browser {
			window = w
		}
		for i, tab := range sp.Tabs {
			places[tab.ID] = layoutPlace{
				Space:     sp.ID,
				Name:      sp.Name,
				Window:    window,
				Position:  i,
				Active:    tab.ID == sp.AgentPane.Active,
				LockedTab: tab.AgentPane.LockedTab,
			}
		}
	}
	return places
}
