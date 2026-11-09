import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { normalizeTerminalBackend } from "../entrypoints/shared/terminalBackend.ts";

test("the terminal backend is Built-in unless Durable is chosen", () => {
    assert.equal(normalizeTerminalBackend(undefined), "builtin");
    assert.equal(normalizeTerminalBackend("builtin"), "builtin");
    assert.equal(normalizeTerminalBackend("durable"), "durable");
    assert.equal(normalizeTerminalBackend("Durable"), "builtin");
    assert.equal(normalizeTerminalBackend(1), "builtin");
});

test("Options offers Built-in first and Durable as a preview", () => {
    const html = readFileSync(new URL("../entrypoints/options.html", import.meta.url), "utf8");
    const select = html.match(/<select id="terminalBackend"[^>]*>([\s\S]*?)<\/select>/);
    assert.ok(select, "no terminalBackend select");
    const options = [...select[1].matchAll(/<option value="([^"]+)">([^<]+)<\/option>/g)].map((m) => [m[1], m[2]]);
    assert.deepEqual(options, [["builtin", "Built-in"], ["durable", "Durable (preview)"]]);
});
