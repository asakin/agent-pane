import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { RELEASE_URL, installCommand, renderSetupScreen, renderUpdateScreen } from "../entrypoints/shared/setup.ts";

const WINDOWS = "Win32";
const MAC = "MacIntel";
const LINUX = "Linux x86_64";

function withDom(t) {
    const { document } = parseHTML("<html><body></body></html>");
    const original = globalThis.document;
    globalThis.document = document;
    t.after(() => { globalThis.document = original; });
}

function withClipboard(t, writeText) {
    const original = Object.getOwnPropertyDescriptor(globalThis, "navigator");
    Object.defineProperty(globalThis, "navigator", { value: { clipboard: { writeText } }, configurable: true });
    t.after(() => {
        if (original) Object.defineProperty(globalThis, "navigator", original);
        else delete globalThis.navigator;
    });
}

test("install command is install.ps1 on Windows and install.sh elsewhere", () => {
    assert.equal(installCommand(WINDOWS), `irm ${RELEASE_URL}/download/install.ps1 | iex`);
    assert.equal(installCommand(MAC), `curl -fsSL ${RELEASE_URL}/download/install.sh | sh`);
    assert.equal(installCommand(LINUX), `curl -fsSL ${RELEASE_URL}/download/install.sh | sh`);
});

test("setup screen explains the program and shows this platform's command", (t) => {
    withDom(t);
    for (const [platform, command, shell] of [[WINDOWS, installCommand(WINDOWS), "PowerShell"], [MAC, installCommand(MAC), "a terminal"]]) {
        const root = renderSetupScreen(undefined, platform);
        assert.match(root.textContent, /needs a small program on this computer/);
        assert.match(root.textContent, /runs the terminals/);
        assert.match(root.textContent, new RegExp(`in ${shell}`));
        assert.equal(root.querySelector(".at-setup__commands code").textContent, command);
        assert.equal(root.querySelector(".at-setup__error"), null);
    }
});

test("setup screen links to the latest release in a new tab", (t) => {
    withDom(t);
    const link = renderSetupScreen(undefined, LINUX).querySelector("a");
    assert.equal(link.getAttribute("href"), RELEASE_URL);
    assert.equal(link.getAttribute("target"), "_blank");
});

test("setup screen copies this platform's command", async (t) => {
    withDom(t);
    const copied = [];
    withClipboard(t, async text => { copied.push(text); });
    const root = renderSetupScreen("Specified native messaging host not found.", WINDOWS);
    assert.match(root.querySelector(".at-setup__error").textContent, /native messaging host not found/);
    const copy = root.querySelector('[data-action="copy"]');
    copy.click();
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.deepEqual(copied, [installCommand(WINDOWS)]);
    assert.equal(copy.textContent, "Copied");
});

test("update screen shows and copies this platform's command", async (t) => {
    withDom(t);
    const copied = [];
    withClipboard(t, async text => { copied.push(text); });
    const root = renderUpdateScreen("0.4.0", "0.4.2", async () => {}, WINDOWS);
    assert.equal(root.querySelector(".at-setup__commands code").textContent, installCommand(WINDOWS));
    root.querySelector('[data-action="copy"]').click();
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.deepEqual(copied, [installCommand(WINDOWS)]);
});
