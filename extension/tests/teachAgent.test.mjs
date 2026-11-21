import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { TEACH_ENTER_DELAY_MS, TEACH_PROMPT, mountTeach, sendTeachPrompt } from "../entrypoints/shared/pagePanel.ts";

const setup = (t) => {
    const { document } = parseHTML(`<html><body><div id="teach"></div></body></html>`);
    const original = globalThis.document;
    globalThis.document = document;
    t.after(() => { globalThis.document = original; });
    const host = document.getElementById("teach");
    const clicks = [];
    const teach = mountTeach(host, async () => { clicks.push("teach"); });
    const button = host.querySelector("button");
    const click = () => button.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));
    return { teach, button, clicks, click };
};

test("Teach agent is disabled until a pane tab is open", async (t) => {
    const { teach, button, clicks, click } = setup(t);
    assert.equal(button.textContent, "Teach agent");
    assert.equal(button.disabled, true, "disabled before any pane tab");
    click();
    assert.deepEqual(clicks, []);

    teach.setEnabled(true);
    assert.equal(button.disabled, false);
    click();
    assert.deepEqual(clicks, ["teach"]);
    await new Promise((resolve) => setImmediate(resolve));

    // The last pane tab closes.
    teach.setEnabled(false);
    click();
    assert.deepEqual(clicks, ["teach"]);
});

test("Teach agent types the prompt into the selected pane tab, then presses Enter", async () => {
    const delivered = [];
    const waits = [];
    const sent = await sendTeachPrompt({
        isDestination: () => true,
        deliver: (bytes) => (delivered.push(bytes), true),
        wait: async (ms) => { waits.push([ms, delivered.length]); },
    });
    assert.equal(sent, "sent");
    assert.equal(TEACH_PROMPT, "Run agent-pane --skill and follow what it prints for the rest of this session.");
    assert.deepEqual(delivered, [TEACH_PROMPT, "\r"]);
    assert.deepEqual(waits, [[TEACH_ENTER_DELAY_MS, 1]], "Enter is its own input, after the prompt");
});

test("Teach agent sends nothing to a pane tab it was not clicked for", async () => {
    const delivered = [];
    let active = "B";
    const steps = {
        isDestination: () => active === "A",
        deliver: (bytes) => (delivered.push([active, bytes]), true),
        wait: async () => {},
    };
    assert.equal(await sendTeachPrompt(steps), "not-sent");
    assert.deepEqual(delivered, []);

    // The person switches away between the prompt and Enter: Enter is not
    // pressed in the other pane tab, and the result says the prompt was typed.
    active = "A";
    assert.equal(await sendTeachPrompt({ ...steps, wait: async () => { active = "B"; } }), "typed");
    assert.deepEqual(delivered, [["A", TEACH_PROMPT]]);
});

test("Teach agent does not press Enter when the prompt was not taken", async () => {
    const delivered = [];
    const sent = await sendTeachPrompt({
        isDestination: () => true,
        deliver: (bytes) => (delivered.push(bytes), false),
        wait: async () => {},
    });
    assert.equal(sent, "not-sent");
    assert.deepEqual(delivered, [TEACH_PROMPT]);
});

test("Teach agent stays disabled until its last click settles", async (t) => {
    const { document } = parseHTML(`<html><body><div id="teach"></div></body></html>`);
    const original = globalThis.document;
    globalThis.document = document;
    t.after(() => { globalThis.document = original; });
    const host = document.getElementById("teach");
    let finish;
    let clicks = 0;
    const teach = mountTeach(host, () => (clicks++, new Promise((resolve) => { finish = resolve; })));
    const button = host.querySelector("button");
    const click = () => button.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));
    teach.setEnabled(true);

    click();
    click();
    assert.equal(clicks, 1, "a second click while teaching does nothing");
    assert.equal(button.disabled, true);
    // A status update while teaching does not enable it early.
    teach.setEnabled(true);
    assert.equal(button.disabled, true);

    finish();
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(button.disabled, false);
    click();
    assert.equal(clicks, 2);
});
