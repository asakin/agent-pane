import assert from "node:assert/strict";
import test from "node:test";
import { bindKeySequences } from "../entrypoints/shared/terminalKeys.ts";

const MAC = "MacIntel";
const WINDOWS = "Win32";
const LINUX = "Linux x86_64";

function harness(platform, bufferType = "normal", selected = false, writeClipboard = async () => undefined) {
    let handler;
    const sent = [];
    const actions = [];
    const copied = [];
    bindKeySequences({
        attachCustomKeyEventHandler: fn => { handler = fn; }, input: data => sent.push(data),
        buffer: { active: { type: bufferType } }, hasSelection: () => selected,
        getSelection: () => (selected ? "selected text" : ""),
        selectAll: () => actions.push("selectAll"), clear: () => actions.push("clear"),
        clearSelection: () => { selected = false; actions.push("clearSelection"); },
        scrollPages: n => actions.push(["pages", n]), scrollLines: n => actions.push(["lines", n]),
        scrollToTop: () => actions.push("top"), scrollToBottom: () => actions.push("bottom"),
    }, platform, async text => { copied.push(text); await writeClipboard(text); });
    return {
        sent,
        actions,
        copied,
        key(key, modifiers = {}, type = "keydown") {
            let prevented = false;
            const passThrough = handler({
                key, type, ctrlKey: false, altKey: false, shiftKey: false, metaKey: false,
                ...modifiers, preventDefault() { prevented = true; },
            });
            return { passThrough, prevented };
        },
    };
}

test("Mac editing keys send Cursor's sequences exactly once per press", () => {
    const cases = [
        ["Enter", { shiftKey: true }, "\x1b[13;2u"],
        ["Enter", { metaKey: true }, "\x1b[13;2u"],
        ["Enter", { shiftKey: true, metaKey: true }, "\x1b[13;2u"],
        ["Backspace", { metaKey: true }, "\x15"],
        ["ArrowLeft", { metaKey: true }, "\x01"],
        ["ArrowRight", { metaKey: true }, "\x05"],
        ["Backspace", { altKey: true }, "\x17"],
        ["Delete", { altKey: true }, "\x1bd"],
        ["ArrowLeft", { altKey: true }, "\x1bb"],
        ["ArrowRight", { altKey: true }, "\x1bf"],
        ["ArrowUp", { altKey: true }, "\x1b[1;5A"],
        ["ArrowDown", { altKey: true }, "\x1b[1;5B"],
        ["g", { ctrlKey: true, altKey: true }, "\x07"],
        ["@", { ctrlKey: true, shiftKey: true, code: "Digit2" }, "\x00"],
        ["^", { ctrlKey: true, shiftKey: true, code: "Digit6" }, "\x1e"],
    ];
    for (const [key, modifiers, sequence] of cases) {
        const h = harness(MAC);
        for (const type of ["keydown", "keypress", "keyup"]) {
            assert.deepEqual(h.key(key, modifiers, type), { passThrough: false, prevented: true });
        }
        assert.deepEqual(h.sent, [sequence], key);
    }
});

test("Mac viewport shortcuts act locally without sending input", () => {
    const h = harness(MAC, "normal", true);
    for (const [key, modifiers] of [
        ["a", { metaKey: true }], ["K", { metaKey: true }],
        ["PageUp", {}], ["PageDown", {}], ["Home", { metaKey: true }], ["End", { metaKey: true }],
        ["PageUp", { metaKey: true, altKey: true }], ["PageDown", { metaKey: true, altKey: true }],
    ]) {
        for (const type of ["keydown", "keypress", "keyup"]) {
            assert.deepEqual(h.key(key, modifiers, type), { passThrough: false, prevented: true });
        }
    }
    assert.deepEqual(h.key("Escape"), { passThrough: false, prevented: true });
    assert.deepEqual(h.sent, []);
    assert.deepEqual(h.actions, ["selectAll", "clear", ["pages", -1], ["pages", 1], "top", "bottom", ["lines", -1], ["lines", 1], "clearSelection"]);
});

test("alternate-screen apps retain page navigation; Escape reaches apps without a selection", () => {
    const h = harness(MAC, "alternate");
    for (const key of ["PageUp", "PageDown", "Escape"]) {
        assert.deepEqual(h.key(key), { passThrough: true, prevented: false });
    }
    assert.deepEqual(h.actions, []);
});

test("ordinary input, clipboard keys, extra modifiers and IME stay with xterm", () => {
    const h = harness(MAC);
    for (const [key, modifiers] of [
        ["Enter", {}], ["Backspace", {}], ["a", {}],
        ["c", { metaKey: true }], ["c", { ctrlKey: true }],
        ["Enter", { ctrlKey: true, shiftKey: true }], ["Enter", { altKey: true }],
        ["ArrowLeft", { metaKey: true, shiftKey: true }],
        ["Enter", { shiftKey: true, isComposing: true }],
        ["Enter", { shiftKey: true, keyCode: 229 }],
    ]) {
        assert.deepEqual(h.key(key, modifiers), { passThrough: true, prevented: false });
    }
    assert.deepEqual(h.sent, []);
});

test("non-Mac platforms retain Meta keys and use Ctrl for word deletion", () => {
    const h = harness(LINUX);
    for (const key of ["Enter", "Backspace", "ArrowLeft", "ArrowRight"]) {
        assert.equal(h.key(key, { metaKey: true }).passThrough, true);
    }
    h.key("Backspace", { ctrlKey: true });
    h.key("Delete", { ctrlKey: true });
    h.key("ArrowLeft", { altKey: true });
    h.key("ArrowRight", { altKey: true });
    h.key("Enter", { shiftKey: true });
    assert.deepEqual(h.sent, ["\x17", "\x1bd", "\x1b[1;5D", "\x1b[1;5C", "\x1b[13;2u"]);
});

// A copy or paste key's outcome: what reached the clipboard or the program,
// and whether the browser keeps its default (the paste event) for it.
function press(platform, key, modifiers, selected) {
    const h = harness(platform, "normal", selected);
    const result = h.key(key, modifiers);
    return { ...result, copied: h.copied, sent: h.sent };
}

const toProgram = { passThrough: true, prevented: false, copied: [], sent: [] };
const toPaste = { passThrough: false, prevented: false, copied: [], sent: [] };
const copiedSelection = { passThrough: false, prevented: true, copied: ["selected text"], sent: [] };
const swallowed = { passThrough: false, prevented: true, copied: [], sent: [] };

test("macOS: Cmd+C copies the selection, Cmd+V pastes, Ctrl+C always reaches the program", () => {
    assert.deepEqual(press(MAC, "c", { metaKey: true }, true), copiedSelection);
    assert.deepEqual(press(MAC, "c", { metaKey: true }, false), toProgram);
    assert.deepEqual(press(MAC, "v", { metaKey: true }, false), toPaste);
    assert.deepEqual(press(MAC, "c", { ctrlKey: true }, true), toProgram);
    assert.deepEqual(press(MAC, "c", { ctrlKey: true }, false), toProgram);
    assert.deepEqual(press(MAC, "v", { ctrlKey: true }, false), toProgram);
});

test("Windows: Ctrl+C copies a selection and interrupts otherwise; Ctrl+V and Ctrl+Shift+C/V work", () => {
    assert.deepEqual(press(WINDOWS, "c", { ctrlKey: true }, true), copiedSelection);
    assert.deepEqual(press(WINDOWS, "c", { ctrlKey: true }, false), toProgram);
    assert.deepEqual(press(WINDOWS, "v", { ctrlKey: true }, false), toPaste);
    assert.deepEqual(press(WINDOWS, "C", { ctrlKey: true, shiftKey: true }, true), copiedSelection);
    assert.deepEqual(press(WINDOWS, "C", { ctrlKey: true, shiftKey: true }, false), swallowed);
    assert.deepEqual(press(WINDOWS, "V", { ctrlKey: true, shiftKey: true }, false), toPaste);
});

test("Windows: copying with Ctrl+C clears the selection, so the next Ctrl+C interrupts", async () => {
    const h = harness(WINDOWS, "normal", true);
    assert.deepEqual(h.key("c", { ctrlKey: true }), { passThrough: false, prevented: true });
    await new Promise(setImmediate);
    assert.deepEqual(h.copied, ["selected text"]);
    assert.deepEqual(h.actions, ["clearSelection"]);
    assert.deepEqual(h.key("c", { ctrlKey: true }, "keyup"), { passThrough: false, prevented: true });
    assert.deepEqual(h.key("c", { ctrlKey: true }), { passThrough: true, prevented: false });
    assert.deepEqual(h.copied, ["selected text"]);
});

test("Windows: holding Ctrl+C after a copy never interrupts", async () => {
    const h = harness(WINDOWS, "normal", true);
    h.key("c", { ctrlKey: true });
    await new Promise(setImmediate);
    assert.deepEqual(h.actions, ["clearSelection"]);
    for (let i = 0; i < 3; i++) {
        assert.deepEqual(h.key("c", { ctrlKey: true, repeat: true }), { passThrough: false, prevented: true });
    }
    assert.deepEqual(h.key("c", { ctrlKey: true }, "keyup"), { passThrough: false, prevented: true });
    assert.deepEqual(h.copied, ["selected text"]);
    assert.deepEqual(h.key("c", { ctrlKey: true }), { passThrough: true, prevented: false });
});

test("Windows: a failed Ctrl+C copy keeps the selection, so pressing again retries the copy", async () => {
    const h = harness(WINDOWS, "normal", true, async () => { throw new Error("denied"); });
    h.key("c", { ctrlKey: true });
    await new Promise(setImmediate);
    assert.deepEqual(h.actions, []);
    assert.deepEqual(h.key("c", { ctrlKey: true }), { passThrough: false, prevented: true });
    assert.deepEqual(h.copied, ["selected text", "selected text"]);
});

test("Cmd+C and Ctrl+Shift+C keep the selection, as their terminals do", async () => {
    for (const [platform, key, modifiers] of [
        [MAC, "c", { metaKey: true }], [WINDOWS, "C", { ctrlKey: true, shiftKey: true }],
        [LINUX, "C", { ctrlKey: true, shiftKey: true }],
    ]) {
        const h = harness(platform, "normal", true);
        h.key(key, modifiers);
        await new Promise(setImmediate);
        assert.deepEqual(h.actions, [], platform);
    }
});

test("Linux: Ctrl+Shift+C/V copy and paste; Ctrl+C and Ctrl+V reach the program", () => {
    assert.deepEqual(press(LINUX, "C", { ctrlKey: true, shiftKey: true }, true), copiedSelection);
    assert.deepEqual(press(LINUX, "C", { ctrlKey: true, shiftKey: true }, false), swallowed);
    assert.deepEqual(press(LINUX, "V", { ctrlKey: true, shiftKey: true }, false), toPaste);
    assert.deepEqual(press(LINUX, "c", { ctrlKey: true }, true), toProgram);
    assert.deepEqual(press(LINUX, "c", { ctrlKey: true }, false), toProgram);
    assert.deepEqual(press(LINUX, "v", { ctrlKey: true }, false), toProgram);
});

test("copy keys copy once per press", () => {
    const h = harness(MAC, "normal", true);
    for (const type of ["keydown", "keypress", "keyup"]) {
        assert.deepEqual(h.key("c", { metaKey: true }, type), { passThrough: false, prevented: true });
    }
    assert.deepEqual(h.copied, ["selected text"]);
});
