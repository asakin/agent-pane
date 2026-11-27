import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { byPosition, groupSpaces, mountRunningTabs, newRunId, openSpace, runningName, spaceName, splitRunning, windowName, windowRun } from "../entrypoints/shared/runningTabs.ts";

const durable = (id, window, extra = {}) => ({ id, durable: true, ...(window ? { window } : {}), ...extra });

test("a panel takes back its window's Durable pane tabs and offers the ones no window shows", () => {
    const sessions = [
        durable("mine", "1@r"),
        durable("held", "1@r"),
        durable("other-open", "2@r"),
        durable("closed-window", "3@r"),
        durable("no-window"),
        durable("last-run", "1@old"),
        { id: "builtin", window: "1@r" },
    ];
    const { restore, offer } = splitRunning(sessions, "1@r", new Set(["held"]), new Set(["2@r"]));
    assert.deepEqual(restore.map((s) => s.id), ["mine"]);
    // A window id from before Chrome restarted names no open window.
    assert.deepEqual(offer.map((s) => s.id), ["closed-window", "no-window", "last-run"]);
});

test("a window's name carries the browser run, so a reused window id is another window", () => {
    assert.equal(windowName(5, "abc"), "5@abc");
    assert.notEqual(windowName(5, "abc"), windowName(5, "def"));
    assert.match(newRunId(), /^[0-9a-f]{12}$/);
});

test("a running pane tab is named by its agent and title", () => {
    assert.equal(runningName({ id: "a", label: "Codex", title: "review" }), "Codex: review");
    assert.equal(runningName({ id: "a", label: "Codex", title: "Codex" }), "Codex");
    assert.equal(runningName({ id: "a" }), "Terminal");
});

test("the Running pane tabs list opens or ends each entry and hides when empty", async () => {
    const { document } = parseHTML(`<html><body><div id="host"></div></body></html>`);
    const host = document.getElementById("host");
    const opened = [];
    const ended = [];
    let refreshed = 0;
    const running = mountRunningTabs(host, {
        onOpen: (s) => opened.push(s.id),
        onOpenSpace: () => {},
        onEnd: (s) => ended.push(s.id),
        onRefresh: async () => { refreshed++; },
    });
    assert.equal(host.hidden, true);

    running.set([durable("a", "", { label: "pi" }), durable("b", "", { label: "Terminal" })]);
    assert.equal(host.hidden, false);
    const button = host.querySelector(".at-running__button");
    assert.equal(button.textContent, "2 running");

    const click = (el) => el.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));
    click(button);
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(refreshed, 1);
    const items = document.querySelectorAll(".at-running__item");
    assert.deepEqual(Array.from(items, (row) => row.querySelector(".at-running__open").textContent), ["pi", "Terminal"]);

    click(items[1].querySelector(".at-running__end"));
    assert.deepEqual(ended, ["b"]);
    assert.equal(document.querySelector(".at-running__menu"), null, "the list closes after a choice");

    click(button);
    await new Promise((resolve) => setTimeout(resolve, 0));
    click(document.querySelector(".at-running__item .at-running__open"));
    assert.deepEqual(opened, ["a"]);

    running.set([]);
    assert.equal(host.hidden, true);
});

const inSpace = (id, space, position, extra = {}) => durable(id, "", { label: id, space: { id: space, window: "4@old", position, ...extra } });

test("offered pane tabs are grouped by space, each in its order", () => {
    const offer = [
        inSpace("b", "sp-1", 1),
        inSpace("x", "sp-2", 0),
        durable("lone", "", { label: "lone" }),
        inSpace("a", "sp-1", 0),
        // A malformed place is no space.
        durable("bad", "", { label: "bad", space: { id: 7, position: "0" } }),
    ];
    const spaces = groupSpaces(offer);
    assert.deepEqual(spaces.map((sp) => [sp.id, sp.tabs.map((s) => s.id)]), [
        ["sp-1", ["a", "b"]],
        ["sp-2", ["x"]],
        [undefined, ["lone"]],
        [undefined, ["bad"]],
    ]);
    assert.equal(spaces[0].window, "4@old");
    assert.equal(spaceName(spaces[0]), "a, b");
    assert.deepEqual(byPosition([inSpace("q", "s", 2), inSpace("p", "s", 0), durable("n")]).map((s) => s.id), ["p", "q", "n"]);
});

test("restoring a space opens its pane tabs in order with the same one active, locks only within one Chrome run", () => {
    const space = groupSpaces([
        inSpace("a", "sp-1", 0, { lockedTab: 11 }),
        inSpace("b", "sp-1", 1, { active: true }),
        inSpace("c", "sp-1", 2, { lockedTab: 12 }),
    ])[0];
    assert.equal(windowRun("4@old"), "old");
    assert.equal(windowRun(undefined), "");

    // After Chrome started again: a new run, so no lock carries.
    const restarted = openSpace(space, "new");
    assert.deepEqual(restarted.tabs.map((t) => t.id), ["a", "b", "c"]);
    assert.equal(restarted.activeId, "b");
    assert.ok(restarted.tabs.every((t) => t.lockedTabId === undefined));

    // Taken into another window of the same run: locks carry.
    const sameRun = openSpace(space, "old");
    assert.deepEqual(sameRun.tabs.map((t) => t.lockedTabId), [11, undefined, 12]);

    // Without an active tab, the first one is.
    assert.equal(openSpace(groupSpaces([inSpace("a", "s", 0), inSpace("b", "s", 1)])[0], "new").activeId, "a");
});

test("the Running pane tabs list opens a whole space at once", async () => {
    const { document } = parseHTML(`<html><body><div id="host"></div></body></html>`);
    const host = document.getElementById("host");
    const spaces = [];
    const opened = [];
    const running = mountRunningTabs(host, {
        onOpen: (s) => opened.push(s.id),
        onOpenSpace: (sp) => spaces.push(sp.tabs.map((s) => s.id)),
        onEnd: () => {},
    });
    running.set([inSpace("b", "sp-1", 1), inSpace("a", "sp-1", 0), durable("lone", "", { label: "lone" })]);
    const click = (el) => el.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));
    click(host.querySelector(".at-running__button"));
    await new Promise((resolve) => setTimeout(resolve, 0));
    const spaceRows = document.querySelectorAll(".at-running__open-space");
    assert.equal(spaceRows.length, 1, "a lone pane tab has no space row");
    assert.equal(spaceRows[0].textContent, "Open all 2: a, b");
    assert.deepEqual(Array.from(document.querySelectorAll(".at-running__item"), (row) => row.dataset.ttyId), ["a", "b", "lone"]);
    click(spaceRows[0]);
    assert.deepEqual(spaces, [["a", "b"]]);

    // Single pane tabs still open one by one.
    click(host.querySelector(".at-running__button"));
    await new Promise((resolve) => setTimeout(resolve, 0));
    click(document.querySelectorAll(".at-running__item .at-running__open")[2]);
    assert.deepEqual(opened, ["lone"]);
});

test("a space one window took leaves the other windows' lists", () => {
    // The host moves each pane tab of a taken space to the window that took it.
    const sessions = [
        durable("a", "3@new", { space: { id: "sp-1", window: "3@new", position: 0 } }),
        durable("b", "3@new", { space: { id: "sp-1", window: "3@new", position: 1 } }),
        durable("c", "", { space: { id: "sp-2", window: "4@old", position: 0 } }),
    ];
    const { offer } = splitRunning(sessions, "5@new", new Set(), new Set(["3@new"]));
    assert.deepEqual(offer.map((s) => s.id), ["c"]);
});
