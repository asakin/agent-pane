import assert from "node:assert/strict";
import test from "node:test";
import { isStatusMessage, parseStatus, parseTtyEvent, statusMark } from "../entrypoints/shared/paneStatus.ts";

test("a tty.event is read with its status and title", () => {
    assert.deepEqual(
        parseTtyEvent({ tty: "a", status: { state: "blocked", msg: "allow?", kind: "permission" }, title: "pi" }),
        { tty: "a", status: { state: "blocked", msg: "allow?", kind: "permission" }, title: "pi" },
    );
    assert.deepEqual(parseTtyEvent({ tty: "a", status: null }), { tty: "a", status: null });
    assert.equal(parseTtyEvent({ status: { state: "done" } }), null, "no session");
    assert.equal(parseTtyEvent(undefined), null);
});

test("an unknown or malformed status shows no mark", () => {
    assert.equal(parseStatus({ state: "napping" }), null);
    assert.equal(parseStatus("working"), null);
    assert.equal(parseStatus(null), null);
    assert.deepEqual(parseStatus({ state: "done", msg: "", kind: 3 }), { state: "done" });
});

test("each status has its own mark and words", () => {
    assert.equal(statusMark(null), null);
    assert.deepEqual(statusMark({ state: "working", msg: "running tests" }), {
        className: "at-tabs__status at-tabs__status--working",
        text: "Working (running tests)",
    });
    assert.equal(statusMark({ state: "blocked", kind: "permission" }).text, "Waiting for you: needs permission");
    assert.equal(statusMark({ state: "blocked", kind: "question" }).text, "Waiting for you: has a question");
    assert.equal(statusMark({ state: "blocked" }).text, "Waiting for you: needs you");
    assert.equal(statusMark({ state: "done" }).text, "Done");
    assert.equal(statusMark({ state: "error" }).className, "at-tabs__status at-tabs__status--error");
    assert.equal(statusMark({ state: "idle" }).text, "Idle");
});

test("only the background's relay is a status message", () => {
    assert.equal(isStatusMessage({ type: "agent-pane.tty-event", event: { tty: "a", status: null } }), true);
    assert.equal(isStatusMessage({ type: "agent-pane.page-read", tty: "a" }), false);
    assert.equal(isStatusMessage({ type: "agent-pane.tty-event" }), false);
});
