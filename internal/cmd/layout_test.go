//go:build unix

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-pane/internal/herdrcore"
)

func newLayoutHarness(t *testing.T, core *fakeCore, browser string) *nativeHarness {
	t.Helper()
	h := newNativeHarness(t, &coreBackend{logger: discardLogger(), dial: func(context.Context) (coreAPI, error) { return core, nil }})
	h.initialize(t, browser)
	return h
}

func shell(window string) map[string]any {
	return map[string]any{"mode": "shell", "cols": 80, "rows": 24, "window": window}
}

func (h *nativeHarness) layoutCall(t *testing.T, params map[string]any) {
	t.Helper()
	if _, msg := h.call(t, "tty.layout", params); msg != "" {
		t.Fatalf("tty.layout: %s", msg)
	}
}

// tabIDs are a space's tab ids in order.
func tabIDs(sp layoutSpace) []string {
	var ids []string
	for _, tab := range sp.Tabs {
		ids = append(ids, tab.ID)
	}
	return ids
}

func tabOf(ep endpointResult) string { return publicID("tab", ep.Pane) }

func spaceFor(t *testing.T, doc layoutDoc, window string) layoutSpace {
	t.Helper()
	for _, sp := range doc.Spaces {
		if sp.AgentPane.Window == window {
			return sp
		}
	}
	t.Fatalf("no space for %s in %+v", window, doc)
	return layoutSpace{}
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLayoutDocumentFollowsDurablePaneTabs(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	c := h.create(t, shell("9@r1"))

	doc := core.layout(t)
	if len(doc.Spaces) != 2 {
		t.Fatalf("spaces %+v", doc.Spaces)
	}
	seven := spaceFor(t, doc, "b1:7@r1")
	if !sameIDs(tabIDs(seven), []string{tabOf(a), tabOf(b)}) || seven.AgentPane.Active != tabOf(a) {
		t.Fatalf("space %+v", seven)
	}
	first := seven.Tabs[0]
	if first.Name != "Terminal" || first.Root.Type != "block" || first.Root.ID != first.ID || first.Root.Content.Terminal != "t1" {
		t.Fatalf("tab %+v", first)
	}
	if nine := spaceFor(t, doc, "b1:9@r1"); !sameIDs(tabIDs(nine), []string{tabOf(c)}) {
		t.Fatalf("space %+v", nine)
	}
	// Neither secret ids nor the page socket are in it.
	raw, _ := json.Marshal(doc)
	for _, secret := range []string{a.ID, a.Pane, core.runs[0].Env[envSocket]} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("document %s holds %q", raw, secret)
		}
	}

	// The side panel reports a new order, active tab and lock.
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": b.ID, "tabs": []map[string]any{{"id": b.ID, "lockedTab": 55}, {"id": a.ID}}})
	seven = spaceFor(t, core.layout(t), "b1:7@r1")
	if !sameIDs(tabIDs(seven), []string{tabOf(b), tabOf(a)}) || seven.AgentPane.Active != tabOf(b) {
		t.Fatalf("space after report %+v", seven)
	}
	if l := seven.Tabs[0].AgentPane.LockedTab; l == nil || *l != 55 || seven.Tabs[1].AgentPane.LockedTab != nil {
		t.Fatalf("locks %+v", seven.Tabs)
	}

	// tty.list says where each sits.
	for _, item := range h.listFacts(t) {
		want := map[string]int{a.ID: 1, b.ID: 0, c.ID: 0}[item.ID]
		if item.Space == nil || item.Space.Position != want {
			t.Fatalf("tty.list item %+v space %+v", item, item.Space)
		}
		if item.ID == b.ID && (!item.Space.Active || item.Space.Window != "7@r1" || item.Space.LockedTab == nil) {
			t.Fatalf("tty.list item %+v space %+v", item, item.Space)
		}
	}

	// Closing a window keeps its space; ending a pane tab takes it out.
	if _, msg := h.call(t, "tty.detach", map[string]any{"id": c.ID, "pane": c.Pane}); msg != "" {
		t.Fatal(msg)
	}
	if nine := spaceFor(t, core.layout(t), "b1:9@r1"); !sameIDs(tabIDs(nine), []string{tabOf(c)}) {
		t.Fatalf("space after detach %+v", nine)
	}
	if _, msg := h.call(t, "tty.destroy", map[string]any{"id": b.ID, "pane": b.Pane}); msg != "" {
		t.Fatal(msg)
	}
	seven = spaceFor(t, core.layout(t), "b1:7@r1")
	if !sameIDs(tabIDs(seven), []string{tabOf(a)}) || seven.AgentPane.Active != tabOf(a) {
		t.Fatalf("space after destroy %+v", seven)
	}
}

func TestBuiltinPaneTabsAreNotInTheLayout(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS terminals start through the spawn agent")
	}
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	ep := h.create(t, shell("7@r1"))
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": ep.ID, "tabs": []map[string]any{{"id": ep.ID}}})
	if _, ok := core.docs[layoutDocName]; ok {
		t.Fatalf("layout %+v", core.layout(t))
	}
}

// After Chrome is killed and started again, a new host offers each space
// whole, in order, with the active tab and the lock, and a window takes it.
func TestLayoutSpacesAreRestoredAfterChromeRestarts(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	noRediscoverWait(t)
	core := newFakeCore()
	first := newLayoutHarness(t, core, "b1")
	a := first.create(t, shell("7@r1"))
	b := first.create(t, shell("7@r1"))
	c := first.create(t, shell("8@r1"))
	first.layoutCall(t, map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": b.ID}, {"id": a.ID, "lockedTab": 12}}})

	h := restartHost(t, first, core, "b1")
	items := h.listFacts(t)
	if len(items) != 3 {
		t.Fatalf("tty.list %+v", items)
	}
	spaces := map[string]string{}
	for _, item := range items {
		if item.Space == nil {
			t.Fatalf("tty.list item %+v has no space", item)
		}
		spaces[item.ID] = item.Space.Space
		switch item.ID {
		case a.ID:
			if item.Space.Position != 1 || !item.Space.Active || item.Space.Window != "7@r1" || item.Space.LockedTab == nil || *item.Space.LockedTab != 12 {
				t.Fatalf("a %+v", item.Space)
			}
		case b.ID:
			if item.Space.Position != 0 || item.Space.Active {
				t.Fatalf("b %+v", item.Space)
			}
		case c.ID:
			if item.Space.Position != 0 || item.Space.Window != "8@r1" {
				t.Fatalf("c %+v", item.Space)
			}
		}
	}
	if spaces[a.ID] != spaces[b.ID] || spaces[a.ID] == spaces[c.ID] {
		t.Fatalf("spaces %v", spaces)
	}

	// A window of the new Chrome run takes the first space whole, without
	// the lock, which named a tab of the old run.
	for _, id := range []string{b.ID, a.ID} {
		if _, msg := h.call(t, "tty.attach", map[string]any{"id": id, "window": "3@r2"}); msg != "" {
			t.Fatal(msg)
		}
	}
	h.layoutCall(t, map[string]any{"window": "3@r2", "active": a.ID, "space": spaces[a.ID], "tabs": []map[string]any{{"id": b.ID}, {"id": a.ID}}})
	doc := core.layout(t)
	if len(doc.Spaces) != 2 {
		t.Fatalf("spaces %+v", doc.Spaces)
	}
	took := spaceFor(t, doc, "b1:3@r2")
	if took.ID != spaces[a.ID] || !sameIDs(tabIDs(took), []string{tabOf(b), tabOf(a)}) || took.AgentPane.Active != tabOf(a) || took.Tabs[1].AgentPane.LockedTab != nil {
		t.Fatalf("taken space %+v", took)
	}
	for _, item := range h.listFacts(t) {
		if (item.ID == a.ID || item.ID == b.ID) && item.Window != "3@r2" {
			t.Fatalf("tty.list item %+v still names its old window", item)
		}
	}
}

// A name a person gives a pane tab is its name in the document and in
// tty.list, and outlives the host; an empty one returns the automatic name.
func TestARenamedPaneTabKeepsItsName(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	noRediscoverWait(t)
	core := newFakeCore()
	first := newLayoutHarness(t, core, "b1")
	a := first.create(t, shell("7@r1"))
	b := first.create(t, shell("7@r1"))
	first.layoutCall(t, map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID, "name": "  review\nbot  "}, {"id": b.ID}}})
	seven := spaceFor(t, core.layout(t), "b1:7@r1")
	if seven.Tabs[0].Name != "review bot" || seven.Tabs[1].Name != "Terminal" {
		t.Fatalf("names %+v", seven.Tabs)
	}

	h := restartHost(t, first, core, "b1")
	for _, item := range h.listFacts(t) {
		want := map[string]string{a.ID: "review bot", b.ID: ""}[item.ID]
		if item.Name != want {
			t.Fatalf("tty.list item %+v, want name %q", item, want)
		}
	}
	if got := spaceFor(t, core.layout(t), "b1:7@r1").Tabs[0].Name; got != "review bot" {
		t.Fatalf("name after restart %q", got)
	}

	h.layoutCall(t, map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID, "name": ""}, {"id": b.ID}}})
	if got := spaceFor(t, core.layout(t), "b1:7@r1").Tabs[0].Name; got != "Terminal" {
		t.Fatalf("name after clearing %q", got)
	}
	records, err := h.sessions.registry.load()
	if err != nil || records[a.ID].Name != "" {
		t.Fatalf("record %+v %v", records[a.ID], err)
	}
}

func TestTabNameIsOneBoundedLine(t *testing.T) {
	if got := tabName(" a\tb\x00c "); got != "a b c" {
		t.Fatalf("tabName %q", got)
	}
	if got := []rune(tabName(strings.Repeat("é", 300))); len(got) != maxTabName {
		t.Fatalf("tabName kept %d runes", len(got))
	}
}

// Two browsers' hosts write the same document at once; neither loses the
// other's spaces.
func TestTwoHostsWriteTheLayoutAtOnce(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	one := newLayoutHarness(t, core, "b1")
	two := newLayoutHarness(t, core, "b2")
	two.sessions.registry = one.sessions.registry
	var wg sync.WaitGroup
	for _, h := range []*nativeHarness{one, two} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 5 {
				if _, msg := h.call(t, "tty.create", shell("1@r1")); msg != "" {
					t.Error(msg)
				}
			}
		}()
	}
	wg.Wait()
	doc := core.layout(t)
	if len(doc.Spaces) != 2 || len(spaceFor(t, doc, "b1:1@r1").Tabs) != 5 || len(spaceFor(t, doc, "b2:1@r1").Tabs) != 5 {
		t.Fatalf("layout %+v", doc)
	}
}

// A write that loses the version race reads again and keeps the other
// writer's change.
func TestALayoutWriteThatLosesARaceReadsAgain(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	other := h.create(t, shell("1@r1"))
	if err := h.sessions.registry.put(paneRecord{ID: "x", Pane: "other", Session: "session-x", Browser: "b2"}); err != nil {
		t.Fatal(err)
	}
	raced := false
	core.set(func(f *fakeCore) {
		f.docSet = func(string) {
			if raced {
				return
			}
			raced = true
			doc := core.layout(t)
			doc.Spaces = append(doc.Spaces, layoutSpace{ID: "sp-b2", Tabs: []layoutTab{{ID: publicID("tab", "other"), Name: "Other"}}, AgentPane: layoutSpaceMeta{Window: "b2:1@r9"}})
			core.putDoc(layoutDocName, doc)
		}
	})
	h.create(t, shell("1@r1"))
	doc := core.layout(t)
	if !raced || len(doc.Spaces) != 2 || len(spaceFor(t, doc, "b1:1@r1").Tabs) != 2 || spaceFor(t, doc, "b2:1@r9").Tabs[0].Name != "Other" {
		t.Fatalf("layout %+v (raced %v, first %s)", doc, raced, tabOf(other))
	}
}

// Another client of the core may write the document. The host takes no id
// from it, drops tabs no record names, adds back its own live pane tabs, and
// puts back its own order and names.
func TestLayoutEditedByAnotherClient(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	c := h.create(t, shell("7@r1"))
	own := spaceFor(t, core.layout(t), "b1:7@r1")

	core.putDoc(layoutDocName, map[string]any{"spaces": []any{
		map[string]any{
			"id": own.ID, "name": "", "folder": "",
			"agentPane": map[string]any{"window": "b1:7@r1", "socket": "/tmp/evil"},
			"tabs": []any{
				// Reordered and renamed, c dropped.
				map[string]any{"id": tabOf(b), "name": "Renamed", "root": map[string]any{"id": "x", "type": "block", "content": map[string]any{"terminal": "t99"}}},
				map[string]any{"id": tabOf(a), "name": "Terminal"},
				// No record names this one.
				map[string]any{"id": "tab-unknown", "name": "Unknown", "agentPane": map[string]any{"session": a.ID}},
			},
		},
		map[string]any{"id": "sp-foreign", "tabs": []any{map[string]any{"id": "tab-also-unknown"}}},
	}})
	if _, msg := h.call(t, "tty.attach", map[string]any{"id": a.ID, "window": "7@r1"}); msg != "" {
		t.Fatal(msg)
	}
	doc := core.layout(t)
	if len(doc.Spaces) != 1 {
		t.Fatalf("layout %+v", doc)
	}
	sp := doc.Spaces[0]
	if !sameIDs(tabIDs(sp), []string{tabOf(a), tabOf(b), tabOf(c)}) {
		t.Fatalf("tabs %v", tabIDs(sp))
	}
	for _, tab := range sp.Tabs {
		if tab.Name != "Terminal" || tab.Root.ID != tab.ID || tab.Root.Content.Terminal == "t99" {
			t.Fatalf("tab %+v", tab)
		}
	}
	raw, _ := json.Marshal(core.layout(t))
	if strings.Contains(string(raw), "/tmp/evil") || strings.Contains(string(raw), a.ID) {
		t.Fatalf("document %s", raw)
	}
	// Nothing the document said reaches tty.list.
	for _, item := range h.listFacts(t) {
		if item.Space == nil || item.Space.Space != own.ID {
			t.Fatalf("tty.list item %+v space %+v", item, item.Space)
		}
	}
}

// Another client may edit the document before the host starts. The host
// takes back its spaces but not a name it never set, and passes a lock on
// to the side panel, which checks the Chrome tab exists before it uses it.
func TestLayoutEditedBeforeTheHostStarts(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	noRediscoverWait(t)
	core := newFakeCore()
	first := newLayoutHarness(t, core, "b1")
	a := first.create(t, shell("7@r1"))
	doc := core.layout(t)
	doc.Spaces[0].Name = "Edited"
	locked := 99
	doc.Spaces[0].Tabs[0].AgentPane.LockedTab = &locked
	core.putDoc(layoutDocName, doc)

	h := restartHost(t, first, core, "b1")
	items := h.listFacts(t)
	if len(items) != 1 || items[0].ID != a.ID || items[0].Space == nil || items[0].Space.LockedTab == nil || *items[0].Space.LockedTab != 99 {
		t.Fatalf("tty.list %+v", items)
	}
	if sp := spaceFor(t, core.layout(t), "b1:7@r1"); sp.Name != "" {
		t.Fatalf("space %+v kept the name another client wrote", sp)
	}
}

// A registry that cannot be read leaves the document as it is: other hosts'
// tabs cannot be told from ended ones without it.
func TestAnUnreadableRegistryDropsNoOtherHostsTabs(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	if err := h.sessions.registry.put(paneRecord{ID: "x", Pane: "other", Session: "session-x", Browser: "b2"}); err != nil {
		t.Fatal(err)
	}
	doc := core.layout(t)
	doc.Spaces = append(doc.Spaces, layoutSpace{ID: "sp-b2", Tabs: []layoutTab{{ID: publicID("tab", "other")}}, AgentPane: layoutSpaceMeta{Window: "b2:1@r9"}})
	core.putDoc(layoutDocName, doc)
	if err := os.WriteFile(h.sessions.registry.path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID, "lockedTab": 3}}})
	if got := spaceFor(t, core.layout(t), "b2:1@r9"); !sameIDs(tabIDs(got), []string{publicID("tab", "other")}) {
		t.Fatalf("other host's space %+v", got)
	}
	// The report waits in the model for the next write that can read the
	// records.
	if err := h.sessions.registry.put(paneRecord{ID: "x", Pane: "other", Session: "session-x", Browser: "b2"}); err != nil {
		t.Fatal(err)
	}
	h.sessions.syncLayout()
	doc = core.layout(t)
	if mine := spaceFor(t, doc, "b1:7@r1"); mine.Tabs[0].AgentPane.LockedTab == nil || *mine.Tabs[0].AgentPane.LockedTab != 3 {
		t.Fatalf("own space %+v", mine)
	}
	if len(spaceFor(t, doc, "b2:1@r9").Tabs) != 1 {
		t.Fatalf("layout %+v", doc)
	}
}

// A report the host could not write, because the document could not be
// read, is written by the next write.
func TestALayoutReportSurvivesAFailedDocumentRead(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	core.set(func(f *fakeCore) { f.docGet = func(string) error { return errors.New("core is busy") } })
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": b.ID, "tabs": []map[string]any{{"id": b.ID}, {"id": a.ID}}})
	core.set(func(f *fakeCore) { f.docGet = nil })
	h.sessions.syncLayout()
	sp := spaceFor(t, core.layout(t), "b1:7@r1")
	if !sameIDs(tabIDs(sp), []string{tabOf(b), tabOf(a)}) || sp.AgentPane.Active != tabOf(b) {
		t.Fatalf("space %+v", sp)
	}
}

// A write that is running carries the changes made meanwhile: their callers
// do not wait for it.
func TestALayoutChangeDoesNotWaitForAWriteInFlight(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	stalled, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	core.set(func(f *fakeCore) {
		f.docGet = func(string) error {
			once.Do(func() {
				close(stalled)
				<-release
			})
			return nil
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.sessions.syncLayout()
	}()
	<-stalled
	report := make(chan string, 1)
	go func() {
		_, msg := h.call(t, "tty.layout", map[string]any{"window": "7@r1", "active": b.ID, "tabs": []map[string]any{{"id": b.ID}, {"id": a.ID}}})
		report <- msg
	}()
	select {
	case msg := <-report:
		if msg != "" {
			t.Fatal(msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tty.layout waited for the write in flight")
	}
	if places := h.sessions.layoutPlaces(); places[tabOf(b)].Position != 0 {
		t.Fatalf("places %+v", places)
	}
	close(release)
	<-done
	if sp := spaceFor(t, core.layout(t), "b1:7@r1"); !sameIDs(tabIDs(sp), []string{tabOf(b), tabOf(a)}) {
		t.Fatalf("space %+v", sp)
	}
}

// Reports made before the document could be read are applied, once it is,
// in the order they came: a space one window took and another took after
// stays with the second, and a lock on a pane tab a later report leaves out
// is kept.
func TestLayoutReportsBeforeTheFirstReadAreAppliedInOrder(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.set(func(f *fakeCore) { f.docGet = func(string) error { return errors.New("core is busy") } })
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	c := h.create(t, shell("8@r1"))
	core.putDoc(layoutDocName, layoutDoc{Spaces: []layoutSpace{{ID: "sp-saved", Tabs: []layoutTab{{ID: tabOf(a)}}, AgentPane: layoutSpaceMeta{Window: "b1:5@r0"}}}})
	h.layoutCall(t, map[string]any{"window": "7@r1", "space": "sp-saved", "active": a.ID, "tabs": []map[string]any{{"id": a.ID}, {"id": b.ID, "lockedTab": 5}}})
	h.layoutCall(t, map[string]any{"window": "8@r1", "space": "sp-saved", "active": c.ID, "tabs": []map[string]any{{"id": c.ID}}})
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID}}})
	core.set(func(f *fakeCore) { f.docGet = nil })
	h.sessions.syncLayout()
	// Replayed in order: 8@r1 took sp-saved after 7@r1 did, and b, which
	// 7@r1's last report leaves out, stays there with its lock.
	doc := core.layout(t)
	got := spaceFor(t, doc, "b1:8@r1")
	if got.ID != "sp-saved" || !sameIDs(tabIDs(got), []string{tabOf(c), tabOf(b)}) {
		t.Fatalf("layout %+v", doc)
	}
	if l := got.Tabs[1].AgentPane.LockedTab; l == nil || *l != 5 {
		t.Fatalf("locks %+v", got.Tabs)
	}
	if sp := spaceFor(t, doc, "b1:7@r1"); sp.ID == "sp-saved" || !sameIDs(tabIDs(sp), []string{tabOf(a)}) {
		t.Fatalf("layout %+v", doc)
	}
}

// Past layoutPendingMax reports waiting for the document, a report is
// refused, so the panel sends it again.
func TestTooManyLayoutReportsBeforeTheFirstReadAreRefused(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	core.set(func(f *fakeCore) { f.docGet = func(string) error { return errors.New("core is busy") } })
	h := newLayoutHarness(t, core, "b1")
	shortLayoutTimers(h.sessions)
	a := h.create(t, shell("7@r1"))
	params := map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID}}}
	for range layoutPendingMax - len(h.sessions.layout.pending) {
		h.layoutCall(t, params)
	}
	if _, msg := h.call(t, "tty.layout", params); msg == "" {
		t.Fatal("tty.layout took a report past the bound")
	}
}

// A report made before the host has taken back its pane tabs is refused:
// its pane tabs cannot all be named yet.
func TestALayoutReportBeforeRediscoveryIsRefused(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	h.sessions.rediscoverMu.Lock()
	h.sessions.rediscovered, h.sessions.lastRediscover = false, time.Now()
	h.sessions.rediscoverMu.Unlock()
	if _, msg := h.call(t, "tty.layout", map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": a.ID}}}); !strings.Contains(msg, errRediscoveryPending) {
		t.Fatalf("tty.layout before rediscovery: %q", msg)
	}
}

// A pane tab that starts while a write is in flight, and the report that
// follows, are both written, the report's lock included.
func TestAPaneTabStartedDuringAWriteKeepsItsReport(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	a := h.create(t, shell("7@r1"))
	stalled, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	core.set(func(f *fakeCore) {
		f.docGet = func(string) error {
			once.Do(func() {
				close(stalled)
				<-release
			})
			return nil
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.sessions.syncLayout()
	}()
	<-stalled
	b := h.create(t, shell("7@r1"))
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": b.ID, "tabs": []map[string]any{{"id": b.ID, "lockedTab": 8}, {"id": a.ID}}})
	close(release)
	<-done
	sp := spaceFor(t, core.layout(t), "b1:7@r1")
	if !sameIDs(tabIDs(sp), []string{tabOf(b), tabOf(a)}) || sp.AgentPane.Active != tabOf(b) {
		t.Fatalf("space %+v", sp)
	}
	if l := sp.Tabs[0].AgentPane.LockedTab; l == nil || *l != 8 {
		t.Fatalf("locks %+v", sp.Tabs)
	}
}

// waitLayoutWriter waits for the last layout write started to end.
func (m *SessionManager) waitLayoutWriter() {
	m.layout.mu.Lock()
	w := m.layout.writer
	m.layout.mu.Unlock()
	if w != nil {
		<-w
	}
}

func shortLayoutTimers(m *SessionManager) {
	m.layout.mu.Lock()
	m.layout.wait, m.layout.retryFirst = 50*time.Millisecond, 10*time.Millisecond
	m.layout.mu.Unlock()
}

// A terminal call that starts a layout write does not wait on a slow core
// past layoutWaitBudget: the write goes on and lands later.
func TestACreateDoesNotWaitOnASlowLayoutWrite(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	shortLayoutTimers(h.sessions)
	a := h.create(t, shell("7@r1"))
	stalled, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	core.set(func(f *fakeCore) {
		f.docGet = func(string) error {
			once.Do(func() {
				close(stalled)
				<-release
			})
			return nil
		}
	})
	created := make(chan endpointResult, 1)
	go func() { created <- h.create(t, shell("7@r1")) }()
	<-stalled
	var b endpointResult
	select {
	case b = <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("tty.create waited for the layout write")
	}
	close(release)
	h.sessions.waitLayoutWriter()
	if sp := spaceFor(t, core.layout(t), "b1:7@r1"); !sameIDs(tabIDs(sp), []string{tabOf(a), tabOf(b)}) {
		t.Fatalf("space %+v", sp)
	}
}

// A write that failed is tried again without another change.
func TestAFailedLayoutWriteIsTriedAgain(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	shortLayoutTimers(h.sessions)
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	core.set(func(f *fakeCore) { f.docGet = func(string) error { return errors.New("core is busy") } })
	h.layoutCall(t, map[string]any{"window": "7@r1", "active": b.ID, "tabs": []map[string]any{{"id": b.ID}, {"id": a.ID}}})
	core.set(func(f *fakeCore) { f.docGet = nil })
	deadline := time.Now().Add(5 * time.Second)
	for {
		sp := spaceFor(t, core.layout(t), "b1:7@r1")
		if sameIDs(tabIDs(sp), []string{tabOf(b), tabOf(a)}) && sp.AgentPane.Active == tabOf(b) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("space %+v", sp)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Close lets a layout write still running end before it lets go of the
// sessions, so the write never records their pane tabs as gone.
func TestCloseWaitsForALayoutWriteInFlight(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	core := newFakeCore()
	h := newLayoutHarness(t, core, "b1")
	shortLayoutTimers(h.sessions)
	a := h.create(t, shell("7@r1"))
	b := h.create(t, shell("7@r1"))
	c := h.create(t, shell("7@r1"))
	stalled, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	core.set(func(f *fakeCore) {
		f.docGet = func(string) error {
			once.Do(func() {
				close(stalled)
				<-release
			})
			return nil
		}
	})
	report := make(chan string, 1)
	go func() {
		_, msg := h.call(t, "tty.layout", map[string]any{"window": "7@r1", "active": a.ID, "tabs": []map[string]any{{"id": c.ID}, {"id": b.ID}, {"id": a.ID}}})
		report <- msg
	}()
	<-stalled
	if msg := <-report; msg != "" {
		t.Fatal(msg)
	}
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		h.sessions.Close()
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a layout write ran")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-closed
	sp := spaceFor(t, core.layout(t), "b1:7@r1")
	if !sameIDs(tabIDs(sp), []string{tabOf(c), tabOf(b), tabOf(a)}) || sp.AgentPane.Active != tabOf(a) {
		t.Fatalf("space %+v", sp)
	}
}

// Another client may put another host's tab into one of this host's
// spaces. The host keeps it, after its own.
func TestAMixedSpaceKeepsTheOtherHostsTabs(t *testing.T) {
	useBackend(t, terminalBackendDurable)
	noRediscoverWait(t)
	core := newFakeCore()
	first := newLayoutHarness(t, core, "b1")
	a := first.create(t, shell("7@r1"))
	if err := first.sessions.registry.put(paneRecord{ID: "x", Pane: "other", Session: "session-x", Browser: "b2"}); err != nil {
		t.Fatal(err)
	}
	doc := core.layout(t)
	doc.Spaces[0].Tabs = append(doc.Spaces[0].Tabs, layoutTab{ID: publicID("tab", "other"), Name: "Other"})
	doc.Spaces[0].AgentPane.Window = "b2:1@r9"
	core.putDoc(layoutDocName, doc)

	h := restartHost(t, first, core, "b1")
	h.listFacts(t)
	sp := core.layout(t).Spaces
	if len(sp) != 1 || !sameIDs(tabIDs(sp[0]), []string{tabOf(a), publicID("tab", "other")}) {
		t.Fatalf("layout %+v", sp)
	}
}

func TestParseLayoutDropsMalformedEntries(t *testing.T) {
	doc := parseLayout(json.RawMessage(`{"spaces":[{"id":"sp-1","tabs":[{"id":"tab-1"},{"id":"tab-1"},{"id":"bad id"}]},{"id":"sp-1"},{"id":""}]}`))
	if len(doc.Spaces) != 1 || !sameIDs(tabIDs(doc.Spaces[0]), []string{"tab-1"}) || doc.Spaces[0].Tabs[0].Root.Type != "block" {
		t.Fatalf("parsed %+v", doc)
	}
	for _, raw := range []string{``, `null`, `[]`, `{"spaces":7}`} {
		if doc := parseLayout(json.RawMessage(raw)); len(doc.Spaces) != 0 {
			t.Fatalf("%q parsed to %+v", raw, doc)
		}
	}
}

// TestLayoutRealCore writes the layout document in the pinned core when
// AGENT_PANE_TEST_HC names it: two browsers' hosts at once, then another
// client's edit that the next write replaces.
func TestLayoutRealCore(t *testing.T) {
	binary := os.Getenv("AGENT_PANE_TEST_HC")
	if binary == "" {
		t.Skip("set AGENT_PANE_TEST_HC to the pinned hc to run against a real core")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.MkdirTemp("", "apcore")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(data) })
	core := herdrcore.Private(data)
	core.Binary = binary
	t.Cleanup(func() {
		cmd := exec.Command(core.Binary, "server", "stop")
		cmd.Env = core.Env(os.Environ())
		cmd.Run()
	})
	useBackend(t, terminalBackendDurable)
	cfg().Set("command", "/bin/sh")
	t.Cleanup(func() { cfg().Delete("command") })

	one := newNativeHarness(t, newCoreBackend(discardLogger(), core))
	one.initialize(t, "b1")
	two := newNativeHarness(t, newCoreBackend(discardLogger(), core))
	two.sessions.registry = one.sessions.registry
	two.initialize(t, "b2")
	// The first start brings the core up.
	a := one.create(t, shell("1@r1"))
	var wg sync.WaitGroup
	for _, h := range []*nativeHarness{one, two} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				if _, msg := h.call(t, "tty.create", shell("1@r1")); msg != "" {
					t.Error(msg)
				}
			}
		}()
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := core.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	read := func() (layoutDoc, uint64) {
		t.Helper()
		d, err := c.DocGet(ctx, layoutDocName)
		if err != nil {
			t.Fatal(err)
		}
		var doc layoutDoc
		if err := json.Unmarshal(d.Value, &doc); err != nil {
			t.Fatalf("%s: %v", d.Value, err)
		}
		return doc, d.Version
	}
	doc, version := read()
	mine := spaceFor(t, doc, "b1:1@r1")
	if len(doc.Spaces) != 2 || len(mine.Tabs) != 4 || len(spaceFor(t, doc, "b2:1@r1").Tabs) != 3 || mine.Tabs[0].ID != tabOf(a) {
		t.Fatalf("layout %+v", doc)
	}
	want := tabIDs(mine)

	// Another client reverses b1's tabs and renames one.
	edited := doc
	for i := range edited.Spaces {
		if edited.Spaces[i].ID == mine.ID {
			tabs := edited.Spaces[i].Tabs
			slices.Reverse(tabs)
			tabs[0].Name = "Renamed"
		}
	}
	if _, err := c.DocSet(ctx, layoutDocName, edited, version); err != nil {
		t.Fatal(err)
	}
	if _, msg := one.call(t, "tty.attach", map[string]any{"id": a.ID, "window": "1@r1"}); msg != "" {
		t.Fatal(msg)
	}
	doc, _ = read()
	mine = spaceFor(t, doc, "b1:1@r1")
	if !sameIDs(tabIDs(mine), want) || mine.Tabs[len(mine.Tabs)-1].Name != "Terminal" {
		t.Fatalf("b1's space after another client's edit %+v", mine)
	}
	if len(spaceFor(t, doc, "b2:1@r1").Tabs) != 3 {
		t.Fatalf("layout %+v", doc)
	}
}
