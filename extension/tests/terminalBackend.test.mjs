import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { normalizeTerminalBackend } from "../entrypoints/shared/terminalBackend.ts";

test("the terminal backend is Durable unless Built-in is chosen", () => {
    assert.equal(normalizeTerminalBackend(undefined), "durable");
    assert.equal(normalizeTerminalBackend("durable"), "durable");
    assert.equal(normalizeTerminalBackend("builtin"), "builtin");
    assert.equal(normalizeTerminalBackend("Builtin"), "durable");
    assert.equal(normalizeTerminalBackend(1), "durable");
});

test("Options offers Durable first, then Built-in", () => {
    const html = readFileSync(new URL("../entrypoints/options.html", import.meta.url), "utf8");
    const select = html.match(/<select id="terminalBackend"[^>]*>([\s\S]*?)<\/select>/);
    assert.ok(select, "no terminalBackend select");
    const options = [...select[1].matchAll(/<option value="([^"]+)">([^<]+)<\/option>/g)].map((m) => [m[1], m[2]]);
    assert.deepEqual(options, [["durable", "Durable"], ["builtin", "Built-in"]]);
});
