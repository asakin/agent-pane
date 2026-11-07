import assert from "node:assert/strict";
import test from "node:test";
import { isHostRequest } from "../entrypoints/shared/hostConnection.ts";
import { checkHost, MIN_HOST_PROTOCOL, MIN_HOST_VERSION } from "../entrypoints/shared/hostVersion.ts";
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

test("a host at the current protocol passes", () => {
    assert.deepEqual(checkHost({ version: "v1.0.0", protocol: MIN_HOST_PROTOCOL }), { ok: true });
    assert.deepEqual(checkHost({ version: "v1.0.0", protocol: MIN_HOST_PROTOCOL + 1 }), { ok: true });
});

test("a host at an older protocol is blocked", () => {
    assert.deepEqual(checkHost({ version: "1.0.0", protocol: 1 }, 2, "2.0.0"), {
        ok: false,
        version: "1.0.0",
        protocol: 1,
        needed: 2,
        neededVersion: "2.0.0",
    });
});

test("a released host that answers {} is blocked", () => {
    assert.deepEqual(checkHost({}), {
        ok: false,
        version: "",
        protocol: null,
        needed: MIN_HOST_PROTOCOL,
        neededVersion: MIN_HOST_VERSION,
    });
    assert.equal(checkHost(undefined).ok, false);
    assert.equal(checkHost({ version: "v0.9.0", protocol: "1" }).ok, false);
});

test("a dev host always passes", () => {
    assert.deepEqual(checkHost({ version: "dev" }), { ok: true });
    assert.deepEqual(checkHost({ version: "dev", protocol: 0 }, 5), { ok: true });
});

test("a host request is told apart from a response", () => {
    assert.equal(isHostRequest({ jsonrpc: "2.0", id: "h", method: "page.approve", params: {} }), true);
    assert.equal(isHostRequest({ jsonrpc: "2.0", id: "h", method: "page.read", result: {} }), false);
    assert.equal(isHostRequest({ jsonrpc: "2.0", id: "h", error: { code: 1, message: "x" } }), false);
    assert.equal(isHostRequest({ jsonrpc: "2.0", id: "", method: "page.read" }), false);
    assert.equal(isHostRequest({ id: "h", method: "page.read" }), false);
});
