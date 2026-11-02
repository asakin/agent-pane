import assert from "node:assert/strict";
import test from "node:test";
import {
    applyTabLifecycle,
    chromeTabUrl,
    closeMissingLocks,
    concernsAgentTab,
    describeUrl,
    markLockedTabClosed,
    replaceLockedTab,
    statusView,
    toggleLock,
    trackedTabId,
    trackMode,
} from "../entrypoints/shared/tabLock.ts";

const state = (...tabs) => ({ tabs, activeId: tabs[0].id });
const tab = (id, lock = {}) => ({ id, app: "", ...lock });

test("an agent tab follows the active Chrome tab until it is locked", () => {
    const s = state(tab("a"), tab("b"));
    assert.equal(trackMode(s.tabs[0]), "following");
    assert.equal(trackedTabId(s.tabs[0], 7), 7);

    const locked = toggleLock(s, "a", 7);
    assert.deepEqual(locked.tabs[0], tab("a", { lockedTabId: 7 }));
    assert.equal(trackMode(locked.tabs[0]), "locked");
    assert.equal(trackedTabId(locked.tabs[0], 9), 7, "switching Chrome tabs keeps the lock");
    assert.deepEqual(locked.tabs[1], tab("b"), "other agent tabs are not locked");
    assert.equal(trackedTabId(locked.tabs[1], 9), 9);

    const unlocked = toggleLock(locked, "a", 7);
    assert.deepEqual(unlocked.tabs[0], tab("a"));
    assert.equal(trackedTabId(unlocked.tabs[0], 9), 9);
});

test("locking with no Chrome tab shown changes nothing", () => {
    const s = state(tab("a"));
    assert.equal(toggleLock(s, "a", undefined), s);
});

test("closing a locked Chrome tab is sticky, including for background agent tabs", () => {
    const s = { tabs: [tab("a"), tab("b", { lockedTabId: 7 }), tab("c", { lockedTabId: 8 })], activeId: "a" };
    const closed = markLockedTabClosed(s, 7);
    assert.equal(trackMode(closed.tabs[1]), "closed");
    assert.equal(trackedTabId(closed.tabs[1], 3), undefined, "does not retarget to the active tab");
    assert.equal(trackMode(closed.tabs[2]), "locked");
    assert.equal(trackMode(closed.tabs[0]), "following");
    assert.equal(markLockedTabClosed(s, 99), s, "unrelated closes leave state alone");

    const view = statusView(closed.tabs[1], { id: 3, url: "https://example.com/" });
    assert.equal(view.mode, "closed");
    assert.equal(view.text, "Tab closed");

    const cleared = toggleLock(closed, "b", 3);
    assert.deepEqual(cleared.tabs[1], tab("b"), "clicking clears the dead lock instead of relocking");
    assert.equal(trackedTabId(cleared.tabs[1], 3), 3);
});

test("tabs.onReplaced moves the lock to the replacement tab id", () => {
    const s = state(tab("a", { lockedTabId: 7 }), tab("b", { lockedTabId: 8 }));
    const next = replaceLockedTab(s, 70, 7);
    assert.equal(next.tabs[0].lockedTabId, 70);
    assert.equal(trackMode(next.tabs[0]), "locked");
    assert.equal(next.tabs[1].lockedTabId, 8);
    assert.equal(trackMode(markLockedTabClosed(next, 7).tabs[0]), "locked");
});

test("a replacement clears a closed mark the reload check set during the swap", () => {
    const s = state(tab("a", { lockedTabId: 7, lockedTabClosed: true }));
    const next = replaceLockedTab(s, 70, 7);
    assert.deepEqual(next.tabs[0], tab("a", { lockedTabId: 70 }));
    assert.equal(trackMode(next.tabs[0]), "locked");
});

test("tab events queued during startup apply in order", () => {
    const s = state(tab("a", { lockedTabId: 7 }), tab("b", { lockedTabId: 8 }), tab("c"));
    const next = applyTabLifecycle(s, [{ removed: 7, added: 70 }, { removed: 8 }, { removed: 70 }]);
    assert.deepEqual(next.tabs[0], tab("a", { lockedTabId: 70, lockedTabClosed: true }));
    assert.equal(trackMode(next.tabs[1]), "closed");
    assert.equal(trackMode(next.tabs[2]), "following");
    assert.equal(applyTabLifecycle(s, []), s);
    assert.equal(applyTabLifecycle(s, [{ removed: 99 }]), s);
});

test("reload marks locks whose Chrome tab is gone as closed", async () => {
    const s = state(tab("a", { lockedTabId: 7 }), tab("b", { lockedTabId: 8 }), tab("c"));
    const asked = [];
    const next = await closeMissingLocks(s, async (id) => {
        asked.push(id);
        return id === 8;
    });
    assert.deepEqual(asked, [7, 8]);
    assert.equal(trackMode(next.tabs[0]), "closed");
    assert.equal(trackMode(next.tabs[1]), "locked");
    assert.equal(trackMode(next.tabs[2]), "following");
    assert.equal(await closeMissingLocks(s, async () => true), s);
});

test("following tabs only react in the panel window; locked tabs follow their id anywhere", () => {
    const following = tab("a");
    assert.equal(concernsAgentTab(following, 1, 7, { id: 7, windowId: 1 }), true);
    assert.equal(concernsAgentTab(following, 1, 7, { id: 7, windowId: 2 }), false);
    assert.equal(concernsAgentTab(following, 1, 7, { id: 8, windowId: 1 }), false);

    const locked = tab("a", { lockedTabId: 9 });
    assert.equal(concernsAgentTab(locked, 1, 7, { id: 9, windowId: 2 }), true);
    assert.equal(concernsAgentTab(locked, 1, 7, { id: 7, windowId: 1 }), false);

    const closed = tab("a", { lockedTabId: 9, lockedTabClosed: true });
    assert.equal(concernsAgentTab(closed, 1, 7, { id: 9, windowId: 1 }), false);
});

test("status text prefers the pending URL and handles special pages", () => {
    assert.equal(chromeTabUrl({ url: "https://old.example/", pendingUrl: "https://new.example/" }), "https://new.example/");
    assert.equal(chromeTabUrl({ url: "https://old.example/" }), "https://old.example/");
    assert.equal(describeUrl(""), "No page");
    assert.equal(describeUrl("chrome://newtab/"), "New Tab");
    assert.equal(describeUrl("about:blank"), "about:blank");
    assert.equal(describeUrl("chrome://settings/"), "chrome://settings/");
    assert.equal(describeUrl("https://example.com/a?b"), "example.com/a?b");

    const view = statusView(tab("a", { lockedTabId: 7 }), { id: 7, url: "https://example.com/x", title: "Example" });
    assert.equal(view.mode, "locked");
    assert.equal(view.text, "example.com/x");
    assert.match(view.tooltip, /https:\/\/example\.com\/x/);
    assert.match(view.tooltip, /Example/);

    assert.equal(statusView(tab("a"), undefined).text, "No tab");
});
