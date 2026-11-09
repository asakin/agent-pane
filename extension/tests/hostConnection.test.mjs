import assert from "node:assert/strict";
import test from "node:test";
import { createHostConnection } from "../entrypoints/shared/hostConnection.ts";
import { HOST_OUTDATED } from "../entrypoints/shared/hostVersion.ts";

const CURRENT = { version: "0.3.0", protocol: 1 };
const RELEASED = {};

function listenerSet() {
    const fns = new Set();
    return {
        addListener(fn) { fns.add(fn); },
        removeListener(fn) { fns.delete(fn); },
        emit(arg) { for (const fn of [...fns]) fn(arg); },
    };
}

// A Chrome native port backed by one fake host process. Like Chrome, it does
// not fire onDisconnect on the side that called disconnect().
function fakeHost(info, { answer = true } = {}) {
    const onMessage = listenerSet();
    const onDisconnect = listenerSet();
    const port = {
        info,
        alive: true,
        sent: [],
        onMessage,
        onDisconnect,
        postMessage(msg) {
            if (!port.alive) throw new Error("disconnected");
            port.sent.push(msg);
            if (!answer) return;
            const result = msg.method === "initialize" ? info : { ok: true, method: msg.method };
            queueMicrotask(() => onMessage.emit({ jsonrpc: "2.0", id: msg.id, result }));
        },
        disconnect() { port.alive = false; },
        crash() { port.alive = false; onDisconnect.emit(); },
    };
    return port;
}

function clock() {
    let now = 0;
    let timers = [];
    return {
        setTimeout(fn, ms) {
            const t = { at: now + ms, fn };
            timers.push(t);
            return t;
        },
        clearTimeout(t) { timers = timers.filter((x) => x !== t); },
        async advance(ms) {
            now += ms;
            for (;;) {
                const due = timers.filter((t) => t.at <= now).sort((a, b) => a.at - b.at)[0];
                if (!due) break;
                timers = timers.filter((t) => t !== due);
                due.fn();
                await flush();
            }
            await flush();
        },
    };
}

async function flush() {
    for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r));
}

// `installed` is the host binary connectNative starts next.
function setup(installed, opts) {
    const timers = clock();
    const env = { installed, opts, ports: [], browserId: async () => "B" };
    const host = createHostConnection({
        connectNative() {
            const port = fakeHost(env.installed, env.opts);
            env.ports.push(port);
            return port;
        },
        browserId: () => env.browserId(),
        extensionVersion: "1.0.0",
        onHostRequest: opts?.onHostRequest,
        onHostNotification: opts?.onHostNotification,
        now: opts?.now,
        timers,
    });
    return { host, timers, env };
}

// Run a call while the fake clock moves past the 100 ms connect wait.
async function run(t, promise, ms = 100) {
    await flush();
    await t.timers.advance(ms);
    return promise;
}

const init = (id, reconnect = false) => ({ jsonrpc: "2.0", id, method: "initialize", params: { reconnect } });
const create = (id) => ({ jsonrpc: "2.0", id, method: "tty.create", params: {} });

test("two windows starting together share one host and one handshake", async () => {
    const t = setup(CURRENT);
    const [a, b] = await run(t, Promise.all([t.host.handle(init("a")), t.host.handle(init("b"))]));
    assert.equal(t.env.ports.length, 1);
    assert.equal(t.env.ports[0].sent.filter((m) => m.method === "initialize").length, 1);
    assert.deepEqual(a, { jsonrpc: "2.0", id: "a", result: CURRENT });
    assert.deepEqual(b, { jsonrpc: "2.0", id: "b", result: CURRENT });
});

test("Retry in a second window keeps the host the first window's Retry started", async () => {
    const t = setup(RELEASED);
    await run(t, Promise.all([t.host.handle(init("a")), t.host.handle(init("b"))]));
    t.env.installed = CURRENT;

    const retryA = await run(t, t.host.handle(init("a2", true)));
    assert.equal(retryA.result.protocol, 1);
    assert.equal(t.env.ports.length, 2);
    assert.equal(t.env.ports[0].alive, false);

    const retryB = await run(t, t.host.handle(init("b2", true)));
    assert.equal(retryB.result.protocol, 1);
    assert.equal(t.env.ports.length, 2);
    assert.equal(t.env.ports[1].alive, true);
});

test("a stale handshake timeout does not close the replacement host", async () => {
    const t = setup(RELEASED, { answer: false });
    const stale = t.host.handle(init("b"));
    await run(t, Promise.resolve());
    t.env.installed = CURRENT;
    t.env.opts = { answer: true };
    // A's Retry gives up on the stalled host and starts the new one.
    const retry = t.host.handle(init("a", true));
    await t.timers.advance(5000);
    await t.timers.advance(100);
    assert.equal((await retry).result.protocol, 1);
    assert.equal((await stale).error.code, -32000);
    assert.equal(t.env.ports.length, 2);
    assert.equal(t.env.ports[1].alive, true);
    const res = await t.host.handle(create("c"));
    assert.equal(res.result.ok, true);
    assert.equal(t.env.ports.length, 2);
});

test("a handshake during the connect wait is not reported as disconnected", async () => {
    const t = setup(CURRENT);
    const warmup = t.host.getPort();
    await flush();
    const panel = t.host.handle(init("a"));
    await t.timers.advance(100);
    await warmup;
    assert.equal((await panel).result.protocol, 1);
    assert.equal(t.env.ports.length, 1);
});

test("a failing browserId read answers with an error and the next call recovers", async () => {
    const t = setup(CURRENT);
    t.env.browserId = async () => { throw new Error("storage unavailable"); };
    const res = await t.host.handle(init("a"));
    assert.deepEqual(res.error, { code: -32001, message: "storage unavailable" });
    assert.equal(t.env.ports.length, 0);

    t.env.browserId = async () => "B";
    const again = await run(t, t.host.handle(init("b")));
    assert.equal(again.result.protocol, 1);
});

test("terminals are refused on a host that fails the check", async () => {
    const t = setup(RELEASED);
    const res = await run(t, t.host.handle(create("a")));
    assert.equal(res.id, "a");
    assert.equal(res.error.code, HOST_OUTDATED);
    assert.equal(t.env.ports[0].sent.some((m) => m.method === "tty.create"), false);
});

test("a host started after a crash is checked before it gets a terminal", async () => {
    const t = setup(CURRENT);
    assert.equal((await run(t, t.host.handle(create("a")))).result.ok, true);
    t.env.ports[0].crash();
    t.env.installed = RELEASED;
    const res = await run(t, t.host.handle(create("b")));
    assert.equal(t.env.ports.length, 2);
    assert.equal(res.error.code, HOST_OUTDATED);
});

test("other calls are not gated on the handshake", async () => {
    const t = setup(RELEASED);
    const res = await run(t, t.host.handle({ jsonrpc: "2.0", id: "a", method: "xterm.getConfig" }));
    assert.equal(res.result.method, "xterm.getConfig");
});

test("a host that is not installed answers not connected and is tried again", async () => {
    const t = setup(CURRENT);
    const missing = t.host.handle(init("a"));
    await flush();
    t.env.ports[0].crash();
    assert.equal((await missing).error.code, -32001);
    const again = await run(t, t.host.handle(init("b")));
    assert.equal(again.result.protocol, 1);
    assert.equal(t.env.ports.length, 2);
});

test("a timeout from a crashed host's handshake leaves the new host alone", async () => {
    const t = setup(RELEASED, { answer: false });
    const stale = t.host.handle(init("b"));
    await run(t, Promise.resolve());
    t.env.ports[0].crash();
    t.env.installed = CURRENT;
    t.env.opts = { answer: true };
    const fresh = await run(t, t.host.handle(init("a")));
    assert.equal(fresh.result.protocol, 1);
    await t.timers.advance(5000);
    assert.equal((await stale).error.code, -32000);
    assert.equal(t.env.ports[1].alive, true);
    assert.equal((await t.host.handle(create("c"))).result.ok, true);
    assert.equal(t.env.ports.length, 2);
});

test("requests from the host are answered on the same port, errors included", async () => {
    const asked = [];
    const t = setup(CURRENT, {
        onHostRequest: async (method, params) => {
            asked.push([method, params]);
            if (method === "page.read") throw new Error("Chrome does not let extensions read chrome:// pages");
            return { tabId: 7, title: "T", url: "https://example.com/" };
        },
    });
    await run(t, t.host.handle(init("a")));
    const port = t.env.ports[0];
    const before = port.sent.length;
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-1", method: "page.resolve", params: { tty: "x" } });
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-2", method: "page.read", params: { tty: "x", tabId: 7 } });
    // A response to one of our own calls is not a request.
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-3", method: "page.read", result: {} });
    await flush();
    assert.deepEqual(asked, [["page.resolve", { tty: "x" }], ["page.read", { tty: "x", tabId: 7 }]]);
    const replies = port.sent.slice(before);
    assert.deepEqual(replies[0], { jsonrpc: "2.0", id: "host-1", result: { tabId: 7, title: "T", url: "https://example.com/" } });
    assert.equal(replies[1].id, "host-2");
    assert.equal(replies[1].error.message, "Chrome does not let extensions read chrome:// pages");
    assert.equal(replies.length, 2);
});

test("a host request that expired before it arrived is refused, not acted on", async () => {
    const asked = [];
    const t = setup(CURRENT, {
        now: () => 10_000,
        onHostRequest: async (method, params) => {
            asked.push(method);
            return { choice: "all" };
        },
    });
    await run(t, t.host.handle(init("a")));
    const port = t.env.ports[0];
    const before = port.sent.length;
    // Written whole after the host gave up on it, as a stalled pipe can.
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-1", method: "page.approve", params: { tty: "x", expiresAt: 9_999 } });
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-2", method: "page.approve", params: { tty: "x", expiresAt: 10_001 } });
    await flush();
    assert.deepEqual(asked, ["page.approve"]);
    const replies = port.sent.slice(before);
    assert.equal(replies[0].id, "host-1");
    assert.match(replies[0].error.message, /^expired/);
    assert.deepEqual(replies[1], { jsonrpc: "2.0", id: "host-2", result: { choice: "all" } });
});

test("notifications from the host are passed on and never answered", async () => {
    const notified = [];
    const asked = [];
    const t = setup(CURRENT, {
        onHostRequest: async (method) => {
            asked.push(method);
            return {};
        },
        onHostNotification: (method, params) => notified.push([method, params]),
    });
    await run(t, t.host.handle(init("a")));
    const port = t.env.ports[0];
    const before = port.sent.length;
    port.onMessage.emit({ jsonrpc: "2.0", method: "tty.event", params: { tty: "x", status: { state: "working" } } });
    // A request has an id; a response has a result.
    port.onMessage.emit({ jsonrpc: "2.0", id: "host-1", method: "page.resolve", params: { tty: "x" } });
    port.onMessage.emit({ jsonrpc: "2.0", method: "tty.event", result: {} });
    await flush();
    assert.deepEqual(notified, [["tty.event", { tty: "x", status: { state: "working" } }]]);
    assert.deepEqual(asked, ["page.resolve"]);
    assert.deepEqual(port.sent.slice(before).map((m) => m.id), ["host-1"]);
});
