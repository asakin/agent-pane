import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "wxt";
import { isOwnExtensionPage } from "../entrypoints/shared/sender.ts";

const extensionRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const ID = "eakooboihfgnikdhdldcmoiafgioeglm";
const ORIGIN = `chrome-extension://${ID}/`;

test("the extension's own pages are answered", () => {
    for (const page of ["sidepanel.html", "options.html", "popup.html", "terminal.html?app=pi"]) {
        assert.equal(isOwnExtensionPage({ id: ID, url: ORIGIN + page }, ID, ORIGIN), true, page);
    }
});

test("content scripts and injected page code are ignored", () => {
    // A content script or executeScript code carries this extension's id and
    // the page URL (Chrome also sets tab and frameId).
    const fromTab = { id: ID, url: "https://example.com/", tab: { id: 4 }, frameId: 0 };
    assert.equal(isOwnExtensionPage(fromTab, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: ID, url: "about:blank", tab: { id: 4 } }, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: ID }, ID, ORIGIN), false);
});

test("other extensions and look-alike origins are ignored", () => {
    const other = "abcdefghijklmnopabcdefghijklmnop";
    assert.equal(isOwnExtensionPage({ id: other, url: `chrome-extension://${other}/x.html` }, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: ID, url: `chrome-extension://${ID}.evil/x.html` }, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: ID, url: `https://evil.example/chrome-extension://${ID}/` }, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage(undefined, ID, ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: "", url: ORIGIN }, "", ORIGIN), false);
    assert.equal(isOwnExtensionPage({ id: ID, url: ORIGIN }, ID, ORIGIN.slice(0, -1)), false);
});

// Build for real: WXT adds content_scripts and web_accessible_resources by
// itself when an entrypoint asks for them, which wxt.config.ts never shows.
async function builtManifest(browser) {
    const outDir = mkdtempSync(join(tmpdir(), "agent-pane-wxt-"));
    try {
        const quiet = () => {};
        await build({
            root: extensionRoot,
            outDir,
            browser,
            logger: { debug: quiet, info: quiet, log: quiet, success: quiet, warn: console.warn, error: console.error, fatal: console.error, level: 0 },
        });
        const dir = readdirSync(outDir).find((d) => d.startsWith(browser + "-"));
        return JSON.parse(readFileSync(join(outDir, dir, "manifest.json"), "utf8"));
    } finally {
        rmSync(outDir, { recursive: true, force: true });
    }
}

test("the built Chrome manifest asks for scripting and all sites, with no page entry points", async () => {
    const manifest = await builtManifest("chrome");
    assert.ok(manifest.permissions.includes("scripting"));
    assert.deepEqual(manifest.host_permissions, ["<all_urls>"]);
    assert.equal(manifest.optional_permissions, undefined);
    assert.equal(manifest.optional_host_permissions, undefined);
    assert.equal(manifest.content_scripts, undefined);
    assert.equal(manifest.web_accessible_resources, undefined);
    assert.equal(manifest.externally_connectable, undefined);
});
