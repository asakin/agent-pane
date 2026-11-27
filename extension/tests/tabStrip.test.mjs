import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { cleanTabName, dropSlot, MAX_TAB_NAME, moveTab, renderTabList, setTabName } from "../entrypoints/shared/tabStrip.ts";

const setup = () => {
    const { document } = parseHTML(`<html><body><div id="list"></div></body></html>`);
    const list = document.getElementById("list");
    const selected = [];
    const closed = [];
    const handlers = { onSelect: (id) => selected.push(id), onClose: (id) => closed.push(id) };
    return { document, list, selected, closed, handlers };
};
const state = (activeId, ...ids) => ({ tabs: ids.map((id) => ({ id, app: "" })), activeId });
const labels = (list) => Array.from(list.children).map((tab) => tab.querySelector(".at-tabs__label").textContent);
const click = (document, el) => el.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));

test("title changes keep the same pane tab buttons, so a click in progress still lands", () => {
    const { document, list, selected, handlers } = setup();
    const titles = new Map([["a", "pi"], ["b", "codex"]]);
    renderTabList(list, state("a", "a", "b"), titles, handlers);
    const [, second] = list.children;

    // A busy agent animates its title between mousedown and mouseup.
    titles.set("a", "⠋ pi working");
    renderTabList(list, state("a", "a", "b"), titles, handlers);
    titles.set("a", "⠙ pi working");
    renderTabList(list, state("a", "a", "b"), titles, handlers);

    assert.equal(list.children[1], second, "the button under the pointer is not replaced");
    assert.deepEqual(labels(list), ["⠙ pi working", "codex"]);
    assert.equal(list.children[0].title, "⠙ pi working");
    click(document, second);
    assert.deepEqual(selected, ["b"]);
});

test("the strip follows the active tab, new tabs, closed tabs and order", () => {
    const { document, list, selected, closed, handlers } = setup();
    const titles = new Map();
    renderTabList(list, state("a", "a", "b", "c"), titles, handlers);
    const [a, b, c] = list.children;
    assert.equal(a.className, "at-tabs__tab at-tabs__tab--active");
    assert.equal(b.className, "at-tabs__tab");

    renderTabList(list, state("c", "c", "a", "d"), titles, handlers);
    assert.deepEqual(Array.from(list.children).map((tab) => tab.dataset.tabId), ["c", "a", "d"]);
    assert.equal(list.children[0], c);
    assert.equal(list.children[1], a);
    assert.equal(c.className, "at-tabs__tab at-tabs__tab--active");
    assert.equal(a.className, "at-tabs__tab");
    assert.equal(b.parentNode, null, "a closed pane tab's button is removed");

    click(document, list.children[2]);
    click(document, a.querySelector(".at-tabs__close"));
    assert.deepEqual(selected, ["d"], "closing does not also select");
    assert.deepEqual(closed, ["a"]);
});

test("a label falls back to a short id and shortens long titles", () => {
    const { list, handlers } = setup();
    renderTabList(list, state("0123456789ab", "0123456789ab", "x"), new Map([["x", "a very long terminal title"]]), handlers);
    assert.deepEqual(labels(list), ["01234567", "a very long ter…"]);
    assert.equal(list.children[0].title, "0123456789ab");
});

const mark = (tab) => tab.querySelector(".at-tabs__status");

test("a pane tab shows its program's status, and loses it when the status clears", () => {
    const { list, handlers } = setup();
    const titles = new Map([["a", "pi"], ["b", "codex"]]);
    const statuses = new Map([["a", { state: "working", msg: "running tests" }]]);
    renderTabList(list, state("a", "a", "b"), titles, handlers, statuses);
    const [a, b] = list.children;
    assert.equal(mark(a).hidden, false);
    assert.equal(mark(a).className, "at-tabs__status at-tabs__status--working");
    assert.equal(mark(a).title, "Working (running tests)");
    assert.equal(a.title, "pi: Working (running tests)");
    assert.equal(mark(b).hidden, true, "no report, no mark");
    assert.equal(b.title, "codex");

    statuses.set("a", { state: "done" });
    renderTabList(list, state("a", "a", "b"), titles, handlers, statuses);
    assert.equal(list.children[0], a, "a status change keeps the button");
    assert.equal(mark(a).className, "at-tabs__status at-tabs__status--done");

    statuses.delete("a");
    renderTabList(list, state("a", "a", "b"), titles, handlers, statuses);
    assert.equal(mark(a).hidden, true);
    assert.equal(a.title, "pi");
});

test("a blocked pane tab in the background draws attention until it is shown", () => {
    const { list, handlers } = setup();
    const statuses = new Map([["b", { state: "blocked", kind: "permission" }]]);
    renderTabList(list, state("a", "a", "b"), new Map(), handlers, statuses);
    const [, b] = list.children;
    assert.equal(b.className, "at-tabs__tab at-tabs__tab--attention");
    assert.equal(mark(b).className, "at-tabs__status at-tabs__status--blocked");
    assert.equal(mark(b).title, "Waiting for you: needs permission");

    renderTabList(list, state("b", "a", "b"), new Map(), handlers, statuses);
    assert.equal(b.className, "at-tabs__tab at-tabs__tab--active");
});

test("a status mark is not taken for the label", () => {
    const { list, handlers } = setup();
    renderTabList(list, state("a", "a"), new Map([["a", "pi"]]), handlers, new Map([["a", { state: "idle" }]]));
    assert.deepEqual(labels(list), ["pi"]);
});

const ids = (s) => s.tabs.map((tab) => tab.id);

test("moving a pane tab puts it at any place and keeps the active one", () => {
    const s = state("b", "a", "b", "c");
    assert.deepEqual(ids(moveTab(s, "c", 0)), ["c", "a", "b"], "to first");
    assert.deepEqual(ids(moveTab(s, "a", 3)), ["b", "c", "a"], "to last");
    assert.deepEqual(ids(moveTab(s, "a", 2)), ["b", "a", "c"]);
    assert.deepEqual(ids(moveTab(s, "c", 1)), ["a", "c", "b"]);
    assert.equal(moveTab(s, "b", 0).activeId, "b");
    assert.equal(moveTab(s, "a", 3).activeId, "b");
    assert.equal(moveTab(s, "b", 1), s, "its own place is no move");
    assert.equal(moveTab(s, "b", 2), s, "just after itself is no move");
    assert.equal(moveTab(s, "a", -1), s, "past the first does not wrap");
    assert.deepEqual(ids(moveTab(s, "a", 9)), ["b", "c", "a"], "past the last lands last");
    assert.equal(moveTab(s, "z", 0), s, "an unknown pane tab");
    assert.deepEqual(ids(s), ["a", "b", "c"], "the old state is not changed");
});

test("a drop lands before the first tab whose middle it is left of", () => {
    const boxes = [{ left: 0, width: 100 }, { left: 100, width: 100 }, { left: 200, width: 100 }];
    assert.equal(dropSlot(boxes, 10), 0);
    assert.equal(dropSlot(boxes, 60), 1);
    assert.equal(dropSlot(boxes, 140), 1);
    assert.equal(dropSlot(boxes, 260), 3);
    assert.equal(dropSlot([], 5), 0);
});

const event = (document, type, props = {}) => Object.assign(new document.defaultView.Event(type, { bubbles: true, cancelable: true }), props);

const moveSetup = (...order) => {
    const ctx = setup();
    let current = state(order[0], ...order);
    const moves = [];
    const render = () => renderTabList(ctx.list, current, new Map(), ctx.handlers);
    ctx.handlers.onMove = (id, slot) => {
        moves.push([id, slot]);
        current = moveTab(current, id, slot);
        render();
    };
    render();
    Array.from(ctx.list.children).forEach((tab, i) => {
        tab.getBoundingClientRect = () => ({ left: i * 100, width: 100 });
    });
    return { ...ctx, moves, order: () => ids(current), tab: (id) => ctx.list.querySelector(`[data-tab-id="${id}"]`) };
};

test("dragging a pane tab shows where it lands and drops it there", () => {
    const { document, list, moves, order, tab } = moveSetup("a", "b", "c");
    assert.equal(tab("a").draggable, true);
    tab("a").dispatchEvent(event(document, "dragstart"));

    const over = event(document, "dragover", { clientX: 280 });
    tab("c").dispatchEvent(over);
    assert.equal(over.defaultPrevented, true, "the strip takes the drop");
    assert.equal(tab("c").dataset.drop, "after");
    assert.equal(tab("a").dataset.drop, undefined);

    tab("c").dispatchEvent(event(document, "dragover", { clientX: 120 }));
    assert.equal(tab("b").dataset.drop, "before");
    assert.equal(tab("c").dataset.drop, undefined);

    tab("c").dispatchEvent(event(document, "drop", { clientX: 290 }));
    assert.deepEqual(moves, [["a", 3]]);
    assert.deepEqual(order(), ["b", "c", "a"]);
    assert.deepEqual(Array.from(list.children).map((t) => t.dataset.tabId), ["b", "c", "a"]);
    assert.ok(Array.from(list.children).every((t) => t.dataset.drop === undefined), "the mark goes with the drop");
    assert.equal(tab("a").className, "at-tabs__tab at-tabs__tab--active", "the active tab stays active");
});

test("a drag that did not start on a pane tab is not taken", () => {
    const { document, moves, tab } = moveSetup("a", "b");
    const over = event(document, "dragover", { clientX: 10 });
    tab("a").dispatchEvent(over);
    assert.equal(over.defaultPrevented, false);
    tab("a").dispatchEvent(event(document, "drop", { clientX: 10 }));
    assert.deepEqual(moves, []);

    tab("b").dispatchEvent(event(document, "dragstart"));
    tab("b").dispatchEvent(event(document, "dragend"));
    tab("a").dispatchEvent(event(document, "drop", { clientX: 10 }));
    assert.deepEqual(moves, [], "a drag that ended elsewhere is over");
});

test("Ctrl+Shift+Left and Right move the focused pane tab and keep it focused", () => {
    const { document, list, moves, order, tab } = moveSetup("a", "b", "c");
    assert.equal(tab("b").getAttribute("aria-keyshortcuts"), "Control+Shift+ArrowLeft Control+Shift+ArrowRight");
    const focused = [];
    for (const t of list.children) {
        t.addEventListener("focus", () => focused.push(t.dataset.tabId));
    }
    const key = (id, k, mods = { ctrlKey: true, shiftKey: true }) => {
        tab(id).dispatchEvent(event(document, "keydown", { key: k, ...mods }));
    };
    key("b", "ArrowLeft");
    assert.deepEqual(order(), ["b", "a", "c"]);
    assert.deepEqual(focused, ["b"], "the moved tab is focused again");
    key("b", "ArrowLeft");
    assert.deepEqual(order(), ["b", "a", "c"], "the first stays first");
    key("a", "ArrowRight");
    key("a", "ArrowRight");
    assert.deepEqual(order(), ["b", "c", "a"], "the last stays last");
    key("c", "ArrowLeft", { shiftKey: true });
    key("c", "ArrowLeft", { ctrlKey: true });
    assert.deepEqual(order(), ["b", "c", "a"], "the shortcut needs both Ctrl and Shift");
    assert.deepEqual(moves, [["b", 0], ["b", -1], ["a", 3], ["a", 4]]);
});

test("a name a person set wins over the terminal's title, and clearing it brings the title back", () => {
    const { list, handlers } = setup();
    const titles = new Map([["a", "⠋ pi working"], ["b", "codex"]]);
    let current = setTabName(state("a", "a", "b"), "a", "  my reviewer\n");
    assert.equal(current.tabs[0].name, "my reviewer");
    renderTabList(list, current, titles, handlers);
    assert.deepEqual(labels(list), ["my reviewer", "codex"]);
    assert.equal(list.children[0].title, "my reviewer");

    // The agent keeps changing the title; the name stays.
    titles.set("a", "⠙ pi working");
    renderTabList(list, current, titles, handlers);
    assert.deepEqual(labels(list), ["my reviewer", "codex"]);

    current = setTabName(current, "a", "   ");
    assert.equal("name" in current.tabs[0], false);
    renderTabList(list, current, titles, handlers);
    assert.deepEqual(labels(list), ["⠙ pi working", "codex"]);
});

test("a pane tab name is one line of at most MAX_TAB_NAME characters", () => {
    assert.equal(cleanTabName("a\tb\u0000c"), "a b c");
    assert.equal(Array.from(cleanTabName("é".repeat(300))).length, MAX_TAB_NAME);
    const before = state("a", "a", "b");
    const after = setTabName(before, "a", "x");
    assert.equal(after.tabs[1], before.tabs[1], "other pane tabs are untouched");
    assert.equal(after.activeId, "a");
});

test("double-clicking a pane tab edits its name: Enter saves, Escape cancels", () => {
    const { document, list, handlers } = setup();
    const renamed = [];
    handlers.onRename = (id, name) => renamed.push([id, name]);
    renderTabList(list, state("a", "a", "b"), new Map([["a", "pi"], ["b", "codex"]]), handlers);
    const [a, b] = list.children;
    const key = (el, k) => {
        const e = new document.defaultView.Event("keydown", { bubbles: true, cancelable: true });
        e.key = k;
        el.dispatchEvent(e);
    };
    const field = () => document.querySelector(".at-tabs__rename");

    a.dispatchEvent(new document.defaultView.Event("dblclick", { bubbles: true }));
    assert.equal(field().value, "pi", "the field starts with what the tab shows");
    field().value = "reviewer";
    key(field(), "Enter");
    assert.equal(field(), null);
    assert.deepEqual(renamed, [["a", "reviewer"]]);

    key(b, "F2");
    field().value = "ignored";
    key(field(), "Escape");
    assert.equal(field(), null);
    assert.deepEqual(renamed, [["a", "reviewer"]], "Escape saves nothing");

    // Saving the text unchanged does not pin the title as a name.
    b.dispatchEvent(new document.defaultView.Event("dblclick", { bubbles: true }));
    key(field(), "Enter");
    assert.deepEqual(renamed, [["a", "reviewer"]]);

    // Double-clicking the close mark does not rename.
    b.querySelector(".at-tabs__close").dispatchEvent(new document.defaultView.Event("dblclick", { bubbles: true }));
    assert.equal(field(), null);
});
