import assert from "node:assert/strict";
import test from "node:test";
import xterm from "@xterm/xterm";
import { attachPtySocket } from "../entrypoints/shared/ptyAttach.ts";

const { Terminal } = xterm;
const write = (terminal, data) => new Promise(resolve => terminal.write(data, resolve));

test("xterm handles chunked synchronized output, alternate screen and reset", async (t) => {
    const terminal = new Terminal({ allowProposedApi: true });
    t.after(() => terminal.dispose());
    await write(terminal, "normal screen");
    await write(terminal, "\x1b[?1049h\x1b[H\x1b[?202");
    await write(terminal, "6hframe");
    assert.equal(terminal.buffer.active.type, "alternate");
    assert.equal(terminal.modes.synchronizedOutputMode, true);
    assert.equal(terminal.buffer.active.getLine(0).translateToString(true), "frame");
    await write(terminal, "\x1b[?2026l\x1b[?1049l");
    assert.equal(terminal.modes.synchronizedOutputMode, false);
    assert.equal(terminal.buffer.active.type, "normal");
    assert.equal(terminal.buffer.active.getLine(0).translateToString(true), "normal screen");
    await write(terminal, "\x1b[?2026h");
    terminal.reset();
    assert.equal(terminal.modes.synchronizedOutputMode, false);
});

test("PTY replay suppresses old terminal replies, then forwards live control bytes", async (t) => {
    const originalWebSocket = globalThis.WebSocket;
    globalThis.WebSocket = { OPEN: 1 };
    t.after(() => { globalThis.WebSocket = originalWebSocket; });
    const socket = new EventTarget();
    socket.readyState = 1;
    const sent = [];
    socket.send = data => sent.push([...data]);
    const terminal = new Terminal();
    const attachment = attachPtySocket(terminal, socket);
    t.after(() => { attachment.dispose(); terminal.dispose(); });
    const receive = data => socket.dispatchEvent(new MessageEvent("message", { data: new TextEncoder().encode(data).buffer }));

    terminal.input("early input");
    receive("\x1b[cold screen");
    await write(terminal, "");
    assert.deepEqual(sent, []);
    terminal.input("\x00\x15");
    assert.deepEqual(sent, [[0, 21]]);
    receive("\x1b[c");
    await write(terminal, "");
    assert.equal(sent.length, 2, "live device-attribute queries still receive a reply");
    assert.equal(sent[1][0], 27);
    attachment.dispose();
    terminal.input("detached input");
    assert.equal(sent.length, 2);
});

test("a refused socket reports one close, and a replay reports ready", (t) => {
    const socket = new EventTarget();
    const terminal = new Terminal();
    let ready = 0;
    let closes = 0;
    const attachment = attachPtySocket(terminal, socket, { onReady: () => ready++, onClose: () => closes++ });
    t.after(() => { attachment.dispose(); terminal.dispose(); });

    socket.dispatchEvent(new MessageEvent("message", { data: new ArrayBuffer(0) }));
    assert.equal(ready, 1);
    socket.dispatchEvent(new Event("error"));
    socket.dispatchEvent(new Event("close"));
    assert.equal(closes, 1, "error then close must reattach once, not twice");
});
