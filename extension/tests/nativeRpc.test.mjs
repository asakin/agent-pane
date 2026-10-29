import assert from "node:assert/strict";
import test from "node:test";
import { CHOOSE_FOLDER_TIMEOUT_MS, errorFromResponse, NATIVE_RPC_TIMEOUT_MS, nativeTimeoutMs, NativeUnavailable, requestNative } from "../entrypoints/shared/nativeRpc.ts";

function fakePort() {
    const listeners = new Set();
    return {
        sent: [],
        postMessage(message) {
            this.sent.push(message);
        },
        onMessage: {
            addListener(fn) { listeners.add(fn); },
            removeListener(fn) { listeners.delete(fn); },
        },
        emit(message) {
            for (const fn of [...listeners]) fn(message);
        },
        listenerCount() { return listeners.size; },
    };
}

function clock() {
    let pending = null;
    return {
        setTimeout(fn) {
            pending = fn;
            return 1;
        },
        clearTimeout() {
            pending = null;
        },
        fire() {
            const fn = pending;
            pending = null;
            fn?.();
        },
        pending() { return pending != null; },
    };
}

function ok(id) {
    return { jsonrpc: "2.0", id, result: { ok: true } };
}

test("the folder picker is allowed to wait", () => {
    assert.equal(nativeTimeoutMs("dialog.chooseFolder"), CHOOSE_FOLDER_TIMEOUT_MS);
    assert.ok(CHOOSE_FOLDER_TIMEOUT_MS > NATIVE_RPC_TIMEOUT_MS);
    assert.equal(nativeTimeoutMs("tty.create"), NATIVE_RPC_TIMEOUT_MS);
});

test("a matching response settles the call and drops the listener", async () => {
    const port = fakePort();
    const timers = clock();
    const pending = requestNative(port, { id: "a", method: "tty.attach" }, 5000, timers);
    port.emit(ok("other"));
    assert.equal(port.listenerCount(), 1);
    port.emit(ok("a"));
    const response = await pending;
    assert.equal(response.result.ok, true);
    assert.equal(port.listenerCount(), 0);
    assert.equal(timers.pending(), false);
});

test("a dead port resolves as disconnected and drops the listener", async () => {
    const port = fakePort();
    port.postMessage = () => { throw new Error("disconnected"); };
    const timers = clock();
    const response = await requestNative(port, { id: "a" }, 5000, timers);
    assert.equal(response.error.code, -32001);
    assert.equal(port.listenerCount(), 0);
    assert.equal(timers.pending(), false);
});

test("a timeout is not a missing session", () => {
    const timeout = errorFromResponse({ error: { code: -32000, message: "Native host did not respond" } });
    const missing = errorFromResponse({ error: { code: -32603, message: "Internal error: invalid tty ID" } });
    assert.ok(timeout instanceof NativeUnavailable);
    assert.ok(!(missing instanceof NativeUnavailable));
    assert.equal(errorFromResponse({}), null);
});

test("no response resolves with an error and a late reply is ignored", async () => {
    const port = fakePort();
    const timers = clock();
    const pending = requestNative(port, { id: "a" }, 5000, timers);
    timers.fire();
    const response = await pending;
    assert.equal(response.error.message, "Native host did not respond");
    assert.equal(port.listenerCount(), 0);
    port.emit(ok("a"));
    assert.equal(response.error.message, "Native host did not respond");
});
