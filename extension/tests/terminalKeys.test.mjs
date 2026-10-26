import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../entrypoints/shared/terminalKeys.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } });
const { bindKeySequences } = await import(`data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`);

function harness(isMac, bufferType = "normal", selected = false) {
    let handler;
    const sent = [];
    const actions = [];
    bindKeySequences({
        attachCustomKeyEventHandler: fn => { handler = fn; }, input: data => sent.push(data),
        buffer: { active: { type: bufferType } }, hasSelection: () => selected,
        selectAll: () => actions.push("selectAll"), clear: () => actions.push("clear"),
        clearSelection: () => actions.push("clearSelection"),
        scrollPages: n => actions.push(["pages", n]), scrollLines: n => actions.push(["lines", n]),
        scrollToTop: () => actions.push("top"), scrollToBottom: () => actions.push("bottom"),
    }, isMac);
    return {
        sent,
        actions,
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
        const h = harness(true);
        for (const type of ["keydown", "keypress", "keyup"]) {
            assert.deepEqual(h.key(key, modifiers, type), { passThrough: false, prevented: true });
        }
        assert.deepEqual(h.sent, [sequence], key);
    }
});

test("Mac viewport shortcuts act locally without sending input", () => {
    const h = harness(true, "normal", true);
    for (const [key, modifiers] of [
        ["a", { metaKey: true }], ["K", { metaKey: true }], ["Escape", {}],
        ["PageUp", {}], ["PageDown", {}], ["Home", { metaKey: true }], ["End", { metaKey: true }],
        ["PageUp", { metaKey: true, altKey: true }], ["PageDown", { metaKey: true, altKey: true }],
    ]) {
        for (const type of ["keydown", "keypress", "keyup"]) {
            assert.deepEqual(h.key(key, modifiers, type), { passThrough: false, prevented: true });
        }
    }
    assert.deepEqual(h.sent, []);
    assert.deepEqual(h.actions, ["selectAll", "clear", "clearSelection", ["pages", -1], ["pages", 1], "top", "bottom", ["lines", -1], ["lines", 1]]);
});

test("alternate-screen apps retain page navigation; Escape reaches apps without a selection", () => {
    const h = harness(true, "alternate");
    for (const key of ["PageUp", "PageDown", "Escape"]) {
        assert.deepEqual(h.key(key), { passThrough: true, prevented: false });
    }
    assert.deepEqual(h.actions, []);
});

test("ordinary input, clipboard keys, extra modifiers and IME stay with xterm", () => {
    const h = harness(true);
    for (const [key, modifiers] of [
        ["Enter", {}], ["Backspace", {}], ["a", {}],
        ["c", { metaKey: true }], ["v", { metaKey: true }], ["c", { ctrlKey: true }],
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
    const h = harness(false);
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
