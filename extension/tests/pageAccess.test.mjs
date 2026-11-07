import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import xterm from "@xterm/xterm";
import { attachPtySocket } from "../entrypoints/shared/ptyAttach.ts";
import { effectiveBase, isThin, readPage } from "../entrypoints/shared/pageContent.ts";
import {
    approvalChoice,
    createApprovalQueue,
    findTerminal,
    isCancelMessage,
    pasteBytes,
    readTab,
    resolveTab,
    scriptingFailure,
    sendTabContent,
    unreadableReason,
} from "../entrypoints/shared/pageAccess.ts";

const { Terminal } = xterm;

// linkedom has no layout, so innerText is textContent; good enough here.
function page(html) {
    const { document } = parseHTML(html);
    for (const el of [document.body, document.documentElement]) {
        if (el && !("innerText" in el)) {
            Object.defineProperty(el, "innerText", { get() { return this.textContent; } });
        }
    }
    return document;
}

const article = `<html><head><title>Site title</title></head><body>
<nav><a href="/">Home</a> <a href="/about">About</a></nav>
<article><h1>The real headline</h1>
${Array.from({ length: 8 }, (_, i) => `<p>Paragraph ${i} of a long article that goes on about one thing in some detail, with enough words for Readability to treat it as the content of the page.</p>`).join("\n")}
<ul><li>first</li><li>second</li></ul>
<pre><code>const x = 1;</code></pre>
</article>
<footer>Copyright</footer>
</body></html>`;

test("info, selection, text and dom return the right content", () => {
    const doc = page(`<html><head><title>Test page</title></head><body><p>Hello <b>world</b></p></body></html>`);
    assert.deepEqual(readPage(doc, "https://example.com/a", "info"), { content: "https://example.com/a\nTest page" });
    assert.deepEqual(readPage(doc, "https://example.com/a", "selection"), { content: "" });
    assert.equal(readPage(doc, "https://example.com/a", "text").content.trim(), "Hello world");
    const dom = readPage(doc, "https://example.com/a", "dom").content;
    assert.match(dom, /^<html>/);
    assert.match(dom, /<b>world<\/b>/);
    assert.ok("error" in readPage(doc, "https://example.com/a", "cookies"));
});

test("markdown uses the Readability article, with title and URL first", () => {
    const doc = page(article);
    const md = readPage(doc, "https://example.com/post", "markdown").content;
    const [title, url, blank] = md.split("\n");
    assert.equal(title, "# Site title", "Readability's title, from <title>");
    assert.equal(url, "https://example.com/post");
    assert.equal(blank, "");
    assert.match(md, /Paragraph 7 of a long article/);
    assert.match(md, /^- +first$/m, "bullet marker is -");
    assert.match(md, /```\nconst x = 1;\n```/, "fenced code");
    assert.doesNotMatch(md, /Copyright/, "the footer is not part of the article");
    assert.doesNotMatch(md, /\n{3,}/, "three or more newlines collapse to two");
    assert.equal(doc.querySelector("nav") !== null, true, "the page itself is not changed");
});

test("markdown --full converts the whole body", () => {
    const md = readPage(page(article), "https://example.com/post", "markdown", true).content;
    assert.ok(md.startsWith("# Site title\nhttps://example.com/post\n\n"));
    assert.match(md, /Copyright/);
    assert.match(md, /About/);
});

test("a thin Readability result falls back to the whole body", () => {
    assert.equal(isThin(501, 75), true);
    assert.equal(isThin(1000, 150), false, "15% is not thin");
    assert.equal(isThin(500, 1), false, "a short page is never thin");

    // A page that is mostly a long list with one small "article".
    const items = Array.from({ length: 60 }, (_, i) => `<li><a href="/p/${i}">Product number ${i} in the catalogue</a></li>`).join("");
    const doc = page(`<html><head><title>Shop</title></head><body>
<ul class="catalogue">${items}</ul>
<article><p>A short blurb about this shop and the things it sells, long enough to be picked as an article by Readability on its own.</p></article>
</body></html>`);
    const md = readPage(doc, "https://shop.example/", "markdown").content;
    assert.match(md, /Product number 59 in the catalogue/, "the whole body was used");
});

test("pages Chrome does not let extensions script say why", () => {
    assert.match(unreadableReason("chrome://settings/"), /chrome:\/\/ pages/);
    assert.match(unreadableReason("https://chromewebstore.google.com/detail/x"), /Chrome Web Store/);
    assert.match(unreadableReason("https://chrome.google.com/webstore/category"), /Chrome Web Store/);
    assert.match(unreadableReason("chrome-extension://abcdefghijklmnopabcdefghijklmnop/x.html"), /other extensions' pages/);
    assert.equal(unreadableReason("https://example.com/"), null);
    assert.equal(unreadableReason("file:///tmp/a.html"), null);
    assert.match(scriptingFailure("https://example.com/a.pdf", new Error("Cannot access contents of the page")), /PDF viewer/);
    const pdf = page("<html><body></body></html>");
    Object.defineProperty(pdf, "contentType", { value: "application/pdf" });
    assert.match(readPage(pdf, "https://example.com/x", "text").error, /PDF viewer/);
});

test("anything but an explicit allow is a denial", () => {
    assert.equal(approvalChoice({ choice: "tab" }), "tab");
    assert.equal(approvalChoice({ choice: "all" }), "all");
    for (const answer of [undefined, null, {}, { choice: "yes" }, "tab"]) {
        assert.equal(approvalChoice(answer), "deny");
    }
});

const stored = {
    "agent-pane.window.5": { activeId: "a", tabs: [{ id: "a", app: "" }, { id: "b", app: "pi", lockedTabId: 42 }] },
    "agent-pane.window.6": { activeId: "c", tabs: [{ id: "c", app: "", lockedTabId: 9, lockedTabClosed: true }] },
    "agent-pane.ttySession.5": "old",
};

test("the tab is the lock, else the focused tab in the terminal's window", async () => {
    assert.deepEqual(findTerminal(stored, "a"), { found: true, windowId: 5, mode: "following" });
    assert.deepEqual(findTerminal(stored, "b"), { found: true, windowId: 5, mode: "locked", tabId: 42 });
    assert.deepEqual(findTerminal(stored, "zz"), { found: false });

    const queries = [];
    const apis = {
        storageSession: { get: async () => stored },
        tabs: {
            get: async (id) => {
                if (id === 42) return { id: 42, title: "Locked", url: "https://locked.example/" };
                throw new Error("no tab");
            },
            query: async (q) => {
                queries.push(q);
                return [{ id: 7, title: "Focused", url: "https://focused.example/" }];
            },
        },
    };
    assert.deepEqual(await resolveTab(apis, "b"), { tabId: 42, title: "Locked", url: "https://locked.example/" });
    assert.deepEqual(await resolveTab(apis, "a"), { tabId: 7, title: "Focused", url: "https://focused.example/" });
    assert.deepEqual(queries.at(-1), { active: true, windowId: 5 });
    await resolveTab(apis, "zz");
    assert.deepEqual(queries.at(-1), { active: true, lastFocusedWindow: true });
    await assert.rejects(resolveTab(apis, "c"), /locked to this terminal was closed/);
});

test("a read injects the reader into the main frame and returns its result", async () => {
    const calls = [];
    const apis = {
        tabs: { get: async () => ({ id: 3, url: "https://example.com/" }) },
        scripting: {
            executeScript: async (injection) => {
                calls.push(injection);
                return injection.files ? [{}] : [{ result: { content: "page text" } }];
            },
        },
    };
    assert.equal(await readTab(apis, 3, "text", false), "page text");
    assert.deepEqual(calls[0], { target: { tabId: 3, frameIds: [0] }, files: ["/page-reader.js"] });
    assert.deepEqual(calls[1].target, { tabId: 3, frameIds: [0] });
    assert.deepEqual(calls[1].args, ["text", false, null]);
    assert.equal(calls[1].world, undefined, "isolated world, the default");

    const chrome = { ...apis, tabs: { get: async () => ({ id: 3, url: "chrome://extensions/" }) } };
    await assert.rejects(readTab(chrome, 3, "text", false), /chrome:\/\/ pages/);
    const failing = { ...apis, scripting: { executeScript: async (i) => i.files ? [{}] : [{ result: { error: "boom" } }] } };
    await assert.rejects(readTab(failing, 3, "text", false), /boom/);
});

test("Send tab pastes with bracketed paste and does not press Enter", async (t) => {
    const terminal = new Terminal({ allowProposedApi: true });
    t.after(() => terminal.dispose());
    const sent = [];
    terminal.onData((d) => sent.push(d));
    await new Promise((r) => terminal.write("\x1b[?2004h", r));
    assert.equal(terminal.modes.bracketedPasteMode, true);
    terminal.input(pasteBytes("# Title\nhttps://x/\n\nbody\n\n", terminal.modes.bracketedPasteMode), true);
    assert.deepEqual(sent, ["\x1b[200~# Title\rhttps://x/\r\rbody\x1b[201~"]);
});

test("page text cannot end the paste early or press Enter", () => {
    const hostile = "harmless\x1b[201~rm -rf ~\r\n";
    const out = pasteBytes(hostile, true);
    assert.equal(out, "\x1b[200~harmless[201~rm -rf ~\x1b[201~");
    assert.equal(out.split("\x1b").length, 3, "only our own two markers");
    assert.equal(pasteBytes("one line\n", false), "one line");
    assert.throws(() => pasteBytes("two\nlines", false), /bracketed paste/);
});

function fakeTimers() {
    const pending = new Map();
    let next = 1;
    return {
        setTimeout(fn, ms) { const id = next++; pending.set(id, { fn, ms }); return id; },
        clearTimeout(id) { pending.delete(id); },
        fire(id) { const t = pending.get(id); pending.delete(id); t?.fn(); },
        pending,
    };
}

const ask = (id, tty = "a", timeoutMs = 1000) => ({ type: "agent-pane.page-approve", id, tty, agent: "", tabId: 7, title: "", url: "", timeoutMs });

test("an expired prompt leaves the queue and does not take the next answer", async () => {
    const timers = fakeTimers();
    let changes = 0;
    const q = createApprovalQueue(() => changes++, timers);
    const first = q.ask(ask("p1"));
    const second = q.ask(ask("p2", "b"));
    assert.equal(q.current().id, "p1");
    // The host stops waiting for p1: it is taken down, and p2 is shown.
    timers.fire(1);
    assert.equal(await first, "deny");
    assert.equal(q.current().id, "p2");
    // A click on the stale p1 button does nothing; the live prompt is answered on its own.
    q.answer("p1", "all");
    assert.equal(q.current().id, "p2");
    q.answer("p2", "tab");
    assert.equal(await second, "tab");
    assert.equal(q.current(), undefined);
    assert.equal(timers.pending.size, 0);
    assert.equal(changes, 3);
});

test("a cancelled prompt leaves the queue, shown or not", async () => {
    const timers = fakeTimers();
    const q = createApprovalQueue(() => {}, timers);
    const p1 = q.ask(ask("p1"));
    const p2 = q.ask(ask("p2"));
    const p3 = q.ask(ask("p3"));
    q.cancel("a", "p2");
    assert.equal(await p2, "deny");
    assert.equal(q.current().id, "p1");
    q.cancel("other-tty", "p1");
    assert.equal(q.current().id, "p1", "a cancel names its own terminal");
    q.cancel("a", "p1");
    assert.equal(await p1, "deny");
    assert.equal(q.current().id, "p3");
    q.answer("p3", "all");
    assert.equal(await p3, "all");
    assert.equal(timers.pending.size, 0);
    assert.equal(isCancelMessage({ type: "agent-pane.page-cancel", id: "p1", tty: "a" }), true);
    assert.equal(isCancelMessage({ type: "agent-pane.page-cancel", id: "", tty: "a" }), false);
});

test("a prompt without a host timeout still expires", () => {
    const timers = fakeTimers();
    const q = createApprovalQueue(() => {}, timers);
    void q.ask({ ...ask("p1"), timeoutMs: undefined });
    assert.equal([...timers.pending.values()][0].ms, 3 * 60 * 1000);
});

test("Send tab does not deliver when the terminal changes during the last await", async () => {
    let active = "A";
    const delivered = [];
    let release;
    const describing = new Promise((r) => { release = r; });
    const sending = sendTabContent({
        read: async () => "page of A",
        describe: () => describing,
        isDestination: () => active === "A",
        deliver: (c) => (delivered.push([active, c]), true),
    });
    // The person switches to B while the tab's title is looked up.
    await new Promise((r) => setImmediate(r));
    active = "B";
    release("Title");
    assert.equal(await sending, null);
    assert.deepEqual(delivered, []);

    active = "A";
    const sent = await sendTabContent({
        read: async () => "page of A",
        describe: async () => "Title",
        isDestination: () => active === "A",
        deliver: (c) => (delivered.push([active, c]), true),
    });
    assert.equal(sent, "Title");
    assert.deepEqual(delivered, [["A", "page of A"]]);
});

test("Send tab reports nothing sent while a reattached socket is still replaying", async (t) => {
    const originalWebSocket = globalThis.WebSocket;
    globalThis.WebSocket = { OPEN: 1 };
    t.after(() => { globalThis.WebSocket = originalWebSocket; });
    const socket = new EventTarget();
    socket.readyState = 1;
    const sent = [];
    socket.send = (data) => sent.push(new TextDecoder().decode(data));
    const terminal = new Terminal();
    const attachment = attachPtySocket(terminal, socket);
    t.after(() => { attachment.dispose(); terminal.dispose(); });
    const steps = {
        read: async () => "page",
        describe: async () => "Title",
        isDestination: () => true,
        deliver: (c) => attachment.sendInput(c),
    };

    // Open, but the replay frame has not been written: input is dropped,
    // so Send tab must not say it was sent.
    assert.equal(await sendTabContent(steps), null);
    assert.deepEqual(sent, []);

    socket.dispatchEvent(new MessageEvent("message", { data: new TextEncoder().encode("old screen").buffer }));
    await new Promise((resolve) => terminal.write("", resolve));
    assert.equal(await sendTabContent(steps), "Title");
    assert.deepEqual(sent, ["page"]);
});

test("whole-body markdown resolves links and images against the document base", () => {
    const html = `<html><head><title>T</title><base href="/docs/v2/"></head><body>
        <p><a href="guide.html">Guide</a> <a href="#top">Top</a> <a href="https://other.example/x">Other</a></p>
        <p><img src="images/chart.png" alt="Chart"> <a href="/products/1">Product</a></p></body></html>`;
    const doc = page(html);
    assert.equal(effectiveBase(doc, "https://example.com/a/b"), "https://example.com/docs/v2/");
    const out = readPage(doc, "https://example.com/a/b", "markdown", true).content;
    assert.match(out, /\[Guide\]\(https:\/\/example\.com\/docs\/v2\/guide\.html\)/);
    assert.match(out, /!\[Chart\]\(https:\/\/example\.com\/docs\/v2\/images\/chart\.png\)/);
    assert.match(out, /\[Product\]\(https:\/\/example\.com\/products\/1\)/);
    assert.match(out, /\[Other\]\(https:\/\/other\.example\/x\)/);
    assert.match(out, /\[Top\]\(#top\)/);

    // Without <base>, the page's own URL is the base.
    const plain = page(`<html><head><title>T</title></head><body><p><a href="next">Next</a></p></body></html>`);
    assert.equal(effectiveBase(plain, "https://example.com/a/b"), "https://example.com/a/b");
    assert.match(readPage(plain, "https://example.com/a/b", "markdown", true).content, /\(https:\/\/example\.com\/a\/next\)/);
});

test("a prompted read delivers only the document the prompt named", async () => {
    const doc = page(`<html><head><title>T</title></head><body>secret</body></html>`);
    assert.deepEqual(readPage(doc, "https://a.example/", "text", false, "https://a.example/#part"), { content: "secret" });
    assert.match(readPage(doc, "https://b.example/", "text", false, "https://a.example/").error, /moved to another page/);
    assert.deepEqual(readPage(doc, "https://b.example/", "text", false), { content: "secret" });

    // The prompt names the committed document, not a navigation still pending.
    const apis = {
        storageSession: { get: async () => ({}) },
        tabs: { query: async () => [{ id: 7, title: "Old", url: "https://old.example/", pendingUrl: "https://new.example/" }] },
    };
    assert.equal((await resolveTab(apis, "a")).url, "https://old.example/");
});
