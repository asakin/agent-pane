//go:build unix

package cmd

import (
	"context"
	"encoding/json"
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
	core.docSet = func(string) {
		if raced {
			return
		}
		raced = true
		doc := core.layout(t)
		doc.Spaces = append(doc.Spaces, layoutSpace{ID: "sp-b2", Tabs: []layoutTab{{ID: publicID("tab", "other"), Name: "Other"}}, AgentPane: layoutSpaceMeta{Window: "b2:1@r9"}})
		core.putDoc(layoutDocName, doc)
	}
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
