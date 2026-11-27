import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { byPosition, groupSpaces, listedTab, mountRunningTabs, newRunId, openSpace, restoreAtStart, runningName, serialReporter, spaceName, splitRunning, windowName, windowRun } from "../entrypoints/shared/runningTabs.ts";

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
    assert.equal(runningName({ id: "a", label: "Codex", title: "review", name: "my reviewer" }), "my reviewer", "a name a person set wins");
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
/** A tabExists that knows only these Chrome tabs. */
const exists = (...ids) => async (id) => ids.includes(id);

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

test("restoring a space opens its pane tabs in order with the same one active, locks only within one Chrome run", async () => {
    const space = groupSpaces([
        inSpace("a", "sp-1", 0, { lockedTab: 11 }),
        inSpace("b", "sp-1", 1, { active: true }),
        inSpace("c", "sp-1", 2, { lockedTab: 12 }),
    ])[0];
    assert.equal(windowRun("4@old"), "old");
    assert.equal(windowRun(undefined), "");

    // After Chrome started again: a new run, so no lock carries.
    const restarted = await openSpace(space, "new", exists());
    assert.deepEqual(restarted.tabs.map((t) => t.id), ["a", "b", "c"]);
    assert.equal(restarted.activeId, "b");
    assert.ok(restarted.tabs.every((t) => t.lockedTabId === undefined));

    // Taken into another window of the same run: locks carry.
    const sameRun = await openSpace(space, "old", exists(11, 12));
    assert.deepEqual(sameRun.tabs.map((t) => t.lockedTabId), [11, undefined, 12]);
    assert.ok(sameRun.tabs.every((t) => !t.lockedTabClosed));

    // A locked Chrome tab that closed while the space was offered: the lock
    // is marked closed, as the panel's own state is, so no read goes to it.
    const oneGone = await openSpace(space, "old", exists(12));
    assert.deepEqual(oneGone.tabs.map((t) => [t.lockedTabId, t.lockedTabClosed]), [[11, true], [undefined, undefined], [12, undefined]]);

    // Without an active tab, the first one is.
    assert.equal((await openSpace(groupSpaces([inSpace("a", "s", 0), inSpace("b", "s", 1)])[0], "new", exists())).activeId, "a");
});

test("a panel takes back its window's pane tabs at start in their order, with locks checked", async () => {
    const mine = (id, position, extra = {}) => durable(id, "7@r1", { space: { id: "sp-1", window: "7@r1", position, ...extra } });
    const listed = [
        mine("c", 2, { lockedTab: 21 }),
        mine("a", 0, { lockedTab: 20 }),
        mine("b", 1, { active: true }),
        durable("elsewhere", "8@r1"),
        mine("held", 3),
    ];
    const held = { tabs: [{ id: "held", app: "", cwd: "/w" }], activeId: "held" };
    const restored = await restoreAtStart(held, listed, "7@r1", "r1", exists(20));
    assert.deepEqual(restored.tabs.map((t) => t.id), ["held", "a", "b", "c"]);
    assert.equal(restored.tabs[0].cwd, "/w");
    assert.equal(restored.activeId, "held", "the panel's own active tab stays");
    assert.deepEqual(restored.tabs.slice(1).map((t) => [t.lockedTabId, t.lockedTabClosed]), [[20, undefined], [undefined, undefined], [21, true]]);

    // With nothing of its own, the space's active one is active.
    assert.equal((await restoreAtStart(null, listed, "7@r1", "r1", exists())).activeId, "b");
    // After Chrome restarted, no lock carries and none is checked.
    const asked = [];
    const restarted = await restoreAtStart(null, listed, "7@r1", "r2", async (id) => (asked.push(id), true));
    assert.ok(restarted.tabs.every((t) => t.lockedTabId === undefined));
    assert.deepEqual(asked, []);
    assert.equal(await restoreAtStart(held, [durable("elsewhere", "8@r1")], "7@r1", "r1", exists()), null);
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

test("a pane tab taken back in the same run keeps its lock", () => {
    const space = (window, lockedTab) => ({ id: "sp-1", window, position: 0, lockedTab });
    assert.deepEqual(listedTab(durable("a", "7@r1", { agent: "pi", space: space("7@r1", 12) }), "r1"), { id: "a", app: "pi", lockedTabId: 12 });
    assert.deepEqual(listedTab(durable("a", "7@r0", { agent: "pi", space: space("7@r0", 12) }), "r1"), { id: "a", app: "pi" });
    assert.deepEqual(listedTab(durable("a", "7@r1", { agent: "pi" }), "r1"), { id: "a", app: "pi" });
});

test("a pane tab taken back keeps the name a person gave it", () => {
    assert.deepEqual(listedTab(durable("a", "7@r0", { agent: "pi", name: "reviewer" }), "r1"), { id: "a", app: "pi", name: "reviewer" });
    assert.deepEqual(listedTab(durable("a", "7@r0", { name: "" }), "r1"), { id: "a", app: "" });
});

test("layout reports go to the host one at a time, newest last, keeping a taken space", async () => {
    const sent = [];
    const answers = [];
    const report = serialReporter((r) => {
        sent.push(r);
        return new Promise((resolve) => answers.push(resolve));
    });
    report({ window: "w", state: 1 });
    report({ window: "w", state: 2, space: "sp-1" });
    report({ window: "w", state: 3 });
    assert.deepEqual(sent, [{ window: "w", state: 1 }]);
    answers.shift()();
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(sent.slice(1), [{ window: "w", state: 3, space: "sp-1" }]);
    answers.shift()();
    await new Promise((resolve) => setImmediate(resolve));
    report({ window: "w", state: 4 });
    assert.deepEqual(sent.slice(2), [{ window: "w", state: 4 }]);
});

test("a report the host refused is sent again after a wait, unless a newer one goes first", async () => {
    const sent = [];
    const answers = [];
    const report = serialReporter((r) => {
        sent.push(r);
        return new Promise((resolve, reject) => answers.push({ resolve, reject }));
    }, { first: 20, max: 40 });
    const tick = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
    report({ window: "w", state: 1, space: "sp-1" });
    answers.shift().reject(new Error("durable pane tabs are not known yet"));
    await tick(5);
    assert.equal(sent.length, 1, "no resend before the wait");
    await tick(40);
    assert.deepEqual(sent.slice(1), [{ window: "w", state: 1, space: "sp-1" }]);
    // A newer report while a resend waits goes at once, in its place.
    answers.shift().reject(new Error("durable pane tabs are not known yet"));
    await tick(0);
    report({ window: "w", state: 2 });
    assert.deepEqual(sent.slice(2), [{ window: "w", state: 2, space: "sp-1" }]);
    answers.shift().resolve();
    await tick(80);
    assert.equal(sent.length, 3, "nothing is sent again after the host took it");
});

test("a report queued behind the one the host took a space with does not take it again", async () => {
    const sent = [];
    const answers = [];
    const report = serialReporter((r) => {
        sent.push(r);
        return new Promise((resolve) => answers.push(resolve));
    });
    report({ window: "w", state: 1, space: "sp-1" });
    report({ window: "w", state: 2 });
    answers.shift()();
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(sent, [{ window: "w", state: 1, space: "sp-1" }, { window: "w", state: 2 }]);
    // A newer selection made while the older one is in flight stays.
    report({ window: "w", state: 3, space: "sp-2" });
    answers.shift()();
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(sent.slice(2), [{ window: "w", state: 3, space: "sp-2" }]);
});

test("a taken space goes with the next report when the one carrying it failed", async () => {
    const sent = [];
    const answers = [];
    const report = serialReporter((r) => {
        sent.push(r);
        return new Promise((resolve, reject) => answers.push({ resolve, reject }));
    });
    report({ window: "w", state: 1, space: "sp-1" });
    report({ window: "w", state: 2 });
    answers.shift().reject(new Error("host is gone"));
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(sent.slice(1), [{ window: "w", state: 2, space: "sp-1" }]);
    answers.shift().reject(new Error("host is gone"));
    await new Promise((resolve) => setImmediate(resolve));
    report({ window: "w", state: 3 });
    assert.deepEqual(sent.slice(2), [{ window: "w", state: 3, space: "sp-1" }]);
    answers.shift().resolve();
    await new Promise((resolve) => setImmediate(resolve));
    report({ window: "w", state: 4 });
    assert.deepEqual(sent.slice(3), [{ window: "w", state: 4 }]);
    // Taking another space replaces the claim.
    report({ window: "w", state: 5, space: "sp-2" });
    answers.shift().reject(new Error("host is gone"));
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(sent.slice(4), [{ window: "w", state: 5, space: "sp-2" }]);
});
