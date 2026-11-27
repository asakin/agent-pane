package cmd

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

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

// layoutState is the host's side of the layout document. Its fields are
// guarded by mu, which is never held across document I/O.
type layoutState struct {
	mu sync.Mutex
	// spaces are the host's own, read from the document once (loaded) and
	// kept here after, so a change another client writes does not replace
	// them.
	spaces []layoutSpace
	loaded bool
	// pending are the reports made before the document was read, in order.
	pending []layoutChange
	// writer is set while a write runs and closed when it ends; dirty says
	// the model changed since that write read it.
	writer chan struct{}
	dirty  bool
	// retry tries a failed write again after retryDelay.
	retry      *time.Timer
	retryDelay time.Duration
	// wait and retryFirst, when set, replace layoutWaitBudget and
	// layoutRetryFirst.
	wait, retryFirst time.Duration
}

// syncLayout writes the document after the host's sessions changed.
func (m *SessionManager) syncLayout() {
	_ = m.writeLayout(nil)
}

// writeLayout applies change to the host's spaces and writes the document,
// once the host knows every Durable pane tab it holds: before that, tabs
// not taken back yet would be dropped, and a report's pane tabs cannot all
// be named, so a report is refused and the panel sends it again.
func (m *SessionManager) writeLayout(change *layoutChange) error {
	if _, ok := m.durable.(*coreBackend); !ok {
		return nil
	}
	if !m.ensureRediscovered() {
		if change != nil {
			return errors.New(errRediscoveryPending)
		}
		return nil
	}
	return m.writeLayoutNow(change)
}

// layoutWaitBudget is how long a caller that starts a write waits for it.
// Past it the write goes on alone, so a slow core never makes a terminal
// call that already succeeded miss the extension's RPC deadline.
const layoutWaitBudget = time.Second

// layoutRetryFirst and layoutRetryMax bound the wait before a write that
// failed is tried again, doubling each time it fails.
const (
	layoutRetryFirst = time.Second
	layoutRetryMax   = 30 * time.Second
)

// layoutPendingMax bounds the reports that wait for a document not read
// yet. Past it a report fails, and the panel sends it again.
const layoutPendingMax = 1024

// writeLayoutNow applies change to the host's model, then writes the
// document. It fails only when it refuses the change. The model changes
// before any document I/O, so a write that fails loses nothing: it is logged
// and tried again. One write runs at a time: a caller that finds one running
// leaves its change for that write and returns at once, and one that starts
// a write waits for it at most layoutWaitBudget.
func (m *SessionManager) writeLayoutNow(change *layoutChange) error {
	core, ok := m.durable.(*coreBackend)
	if !ok {
		return nil
	}
	live := m.layoutSessions()
	l := &m.layout
	l.mu.Lock()
	m.mu.RLock()
	closing, browser := m.closing, m.browser
	m.mu.RUnlock()
	if closing || (!l.loaded && len(live) == 0) {
		// With no session, nothing to record, and the core need not start.
		l.mu.Unlock()
		return nil
	}
	if change != nil {
		if l.loaded {
			l.spaces = change.apply(l.spaces)
		} else if len(l.pending) < layoutPendingMax {
			// The host's spaces come from the document, read first.
			l.pending = append(l.pending, *change)
		} else {
			l.mu.Unlock()
			return errors.New("too many pane tab layout reports wait for the document to be read")
		}
	}
	l.dirty = true
	if l.writer != nil {
		l.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	l.writer = done
	wait := cmp.Or(l.wait, layoutWaitBudget)
	l.mu.Unlock()
	go func() {
		defer close(done)
		m.drainLayout(core, browser)
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
	return nil
}

// drainLayout writes the model until a write finds it unchanged, fails, or
// the host shuts down. A failed write is tried again later, so a change the
// model holds reaches the document without waiting for another change.
func (m *SessionManager) drainLayout(core *coreBackend, browser string) {
	l := &m.layout
	for {
		err := m.writeLayoutOnce(core, browser)
		if err != nil {
			m.logger.Warn("failed to write the pane tab layout", "error", err)
		}
		l.mu.Lock()
		m.mu.RLock()
		closing := m.closing
		m.mu.RUnlock()
		if err != nil {
			l.dirty = true
			if l.retry == nil && !closing {
				l.retryDelay = min(max(2*l.retryDelay, cmp.Or(l.retryFirst, layoutRetryFirst)), layoutRetryMax)
				l.retry = time.AfterFunc(l.retryDelay, func() {
					l.mu.Lock()
					l.retry = nil
					l.mu.Unlock()
					m.syncLayout()
				})
			}
		} else {
			l.retryDelay = 0
		}
		if err != nil || !l.dirty || closing {
			l.writer = nil
			l.mu.Unlock()
			return
		}
		l.mu.Unlock()
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
	l := &m.layout
	for range layoutWriteAttempts {
		l.mu.Lock()
		l.dirty = false
		l.mu.Unlock()
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
		l.mu.Lock()
		// Taken while the model cannot change, so every pane tab a change in
		// the model names is in it: a session that starts later is written
		// by the next pass.
		live := m.layoutSessions()
		if !l.loaded {
			l.spaces = ownSpaces(read, browser, live)
			for _, change := range l.pending {
				l.spaces = change.apply(l.spaces)
			}
			l.pending = nil
			l.loaded = true
		}
		l.spaces = reconcileSpaces(l.spaces, live)
		next := mergeLayout(read, l.spaces, live, recorded)
		l.mu.Unlock()
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
			// The host names no space: a name another client wrote is not
			// kept.
			sp.Name = ""
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
	added := slices.SortedFunc(maps.Values(live), func(a, b layoutSession) int { return strings.Compare(a.tab, b.tab) })
	for _, ls := range added {
		if placed[ls.tab] {
			continue
		}
		i := slices.IndexFunc(out, func(sp layoutSpace) bool { return sp.AgentPane.Window == ls.window })
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
		if slices.IndexFunc(sp.Tabs, func(t layoutTab) bool { return t.ID == sp.AgentPane.Active }) < 0 {
			sp.AgentPane.Active = sp.Tabs[0].ID
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
		target = slices.IndexFunc(spaces, func(sp layoutSpace) bool { return sp.ID == c.space })
	}
	if target < 0 {
		target = slices.IndexFunc(spaces, func(sp layoutSpace) bool { return sp.AgentPane.Window == c.window })
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
	m.layout.mu.Lock()
	defer m.layout.mu.Unlock()
	m.mu.RLock()
	browser := m.browser
	m.mu.RUnlock()
	places := map[string]layoutPlace{}
	for _, sp := range m.layout.spaces {
		window := ""
		if b, w, ok := strings.Cut(sp.AgentPane.Window, ":"); ok && b == browser {
			window = w
		}
		for i, tab := range sp.Tabs {
			places[tab.ID] = layoutPlace{
				Space:     sp.ID,
				Window:    window,
				Position:  i,
				Active:    tab.ID == sp.AgentPane.Active,
				LockedTab: tab.AgentPane.LockedTab,
			}
		}
	}
	return places
}
