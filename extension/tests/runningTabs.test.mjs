import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { mountRunningTabs, newRunId, runningName, splitRunning, windowName } from "../entrypoints/shared/runningTabs.ts";

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
