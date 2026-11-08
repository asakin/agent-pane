import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import {
    applyBlockedDomainEdit,
    blockedDomainsFromConfig,
    createBlockedDomainEditor,
    blockingDomain,
    normalizeBlockedDomains,
    parseBlockedDomain,
} from "../entrypoints/shared/blockedDomains.ts";
import { readPage } from "../entrypoints/shared/pageContent.ts";
import { PAGE_FORMATS } from "../entrypoints/shared/pageContent.ts";
import { assertTabNotBlocked, readTab, readTabDocument, resolveTab, sendTabContent } from "../entrypoints/shared/pageAccess.ts";

const SECRET = "account 12345 balance";

function doc() {
    const { document } = parseHTML(`<html><head><title>Bank</title></head><body><p>${SECRET}</p></body></html>`);
    if (!("innerText" in document.body)) {
        Object.defineProperty(document.body, "innerText", { get() { return this.textContent; } });
    }
    return document;
}

test("an entry blocks its host and subdomains, not lookalikes", () => {
    const list = ["bank.example"];
    for (const url of [
        "https://bank.example/",
        "https://login.bank.example/x",
        "https://a.b.login.bank.example/",
        "https://BANK.Example./",
        "https://bank.example.:8443/path?q#h",
        "http://bank.example:80/",
    ]) {
        assert.equal(blockingDomain(url, list), "bank.example", url);
    }
    for (const url of [
        "https://notbank.example/",
        "https://bank.example.evil.test/",
        "https://bankexample/",
        "https://example/",
        "chrome://settings",
        "not a url",
        "",
    ]) {
        assert.equal(blockingDomain(url, list), null, url);
    }
    assert.equal(blockingDomain("https://bank.example/", []), null);
});

test("entries are validated and normalized", () => {
    assert.deepEqual(parseBlockedDomain(" Bank.Example. "), { domain: "bank.example" });
    assert.deepEqual(parseBlockedDomain("bank.example:8443"), { domain: "bank.example" });
    assert.deepEqual(parseBlockedDomain("https://Login.Bank.Example:8443/home?x=1"), { domain: "login.bank.example" });
    for (const bad of ["", "   ", "*.bank.example", "bank example", "bank.example/path", "..", ".bank.example", "a..b", "http://"]) {
        assert.ok("error" in parseBlockedDomain(bad), JSON.stringify(bad));
    }
    assert.deepEqual(normalizeBlockedDomains(["Bank.example", "bank.example.", 7, "*bad", "other.test"]), ["bank.example", "other.test"]);
    assert.deepEqual(normalizeBlockedDomains(undefined), []);
    assert.deepEqual(normalizeBlockedDomains("bank.example"), []);
});

test("the list comes from config.get, and an unknown policy stops the read", () => {
    assert.deepEqual(blockedDomainsFromConfig({ result: { config: { blockedDomains: ["Bank.Example"] } } }), ["bank.example"]);
    assert.deepEqual(blockedDomainsFromConfig({ result: { config: {} } }), []);
    assert.throws(() => blockedDomainsFromConfig({ error: { message: "Native host is not connected" } }), /Blocked domains: Native host/);
    assert.throws(() => blockedDomainsFromConfig(undefined), /Blocked domains/);
});

test("the reader refuses a blocked document in every format", () => {
    for (const format of PAGE_FORMATS) {
        const out = readPage(doc(), "https://login.bank.example/", format, false, undefined, ["bank.example"]);
        assert.match(out.error, /^blocked: .*bank\.example/, format);
        assert.ok(!JSON.stringify(out).includes(SECRET), format);
        assert.ok(!JSON.stringify(out).includes("Bank</"), format);
    }
    assert.match(readPage(doc(), "https://notbank.example/", "text", false, undefined, ["bank.example"]).content, /account/);
});

/** A tab whose URL, and the list, the test changes between steps. */
function fakeChrome({ url, pendingUrl, blocked = [], documentUrl }) {
    const state = { url, pendingUrl, blocked: [...blocked], documentUrl: documentUrl ?? url, policyReads: 0, injected: 0, onRead: () => {} };
    const apis = {
        blockedDomains: async () => (state.policyReads++, [...state.blocked]),
        storageSession: { get: async () => ({}) },
        tabs: {
            get: async (id) => ({ id, title: "Bank", url: state.url, pendingUrl: state.pendingUrl }),
            query: async () => [{ id: 7, title: "Bank", url: state.url, pendingUrl: state.pendingUrl }],
        },
        scripting: {
            executeScript: async (injection) => {
                if (injection.files) {
                    state.injected++;
                    return [{}];
                }
                state.onRead();
                const [format, full, expect, block] = injection.args;
                const result = readPage(doc(), state.documentUrl, format, full, expect ?? undefined, block);
                return [{ result: { ...result, url: state.documentUrl } }];
            },
        },
    };
    return { state, apis };
}

test("a blocked tab is refused before a prompt and before the reader runs", async () => {
    const { state, apis } = fakeChrome({ url: "https://login.bank.example/", blocked: ["bank.example"] });
    await assert.rejects(resolveTab(apis, "a"), /^Error: blocked: /);
    await assert.rejects(assertTabNotBlocked(apis, 7), /^Error: blocked: /);
    for (const format of PAGE_FORMATS) {
        await assert.rejects(readTab(apis, 7, format, false), /^Error: blocked: /);
    }
    assert.equal(state.injected, 0);

    // A navigation still pending to a blocked page is refused too.
    const pending = fakeChrome({ url: "https://news.example/", pendingUrl: "https://bank.example/", blocked: ["bank.example"] });
    await assert.rejects(resolveTab(pending.apis, "a"), /blocked/);
    await assert.rejects(readTab(pending.apis, 7, "text", false), /blocked/);
});

test("a redirect to a blocked page between the check and the read delivers nothing", async () => {
    // The tab still says news.example, but the document the reader runs in
    // is the bank's.
    const { apis } = fakeChrome({ url: "https://news.example/", documentUrl: "https://login.bank.example/", blocked: ["bank.example"] });
    await assert.rejects(readTab(apis, 7, "markdown", false), /blocked/);

    // The tab moves to a blocked page while the read runs.
    const moving = fakeChrome({ url: "https://news.example/", blocked: ["bank.example"] });
    moving.state.onRead = () => { moving.state.url = "https://bank.example/"; };
    await assert.rejects(readTab(moving.apis, 7, "text", false), /blocked/);
});

test("a block added while a read runs stops its delivery", async () => {
    const { state, apis } = fakeChrome({ url: "https://bank.example/" });
    state.onRead = () => { state.blocked = ["bank.example"]; };
    let error;
    try {
        await readTab(apis, 7, "dom", false);
    } catch (err) {
        error = err;
    }
    assert.match(error.message, /blocked/);
    assert.ok(!error.message.includes(SECRET));
});

test("the policy is read at each read: removing a block restores reading", async () => {
    const { state, apis } = fakeChrome({ url: "https://bank.example/", blocked: ["bank.example"] });
    await assert.rejects(readTab(apis, 7, "text", false), /blocked/);
    state.blocked = [];
    assert.match(await readTab(apis, 7, "text", false), /account/);
    state.blocked = ["Bank.Example."];
    await assert.rejects(readTab(apis, 7, "text", false), /blocked/);
    assert.ok(state.policyReads >= 4);
});

test("a reader result without its document's URL is not delivered", async () => {
    const { apis } = fakeChrome({ url: "https://news.example/" });
    apis.scripting.executeScript = async (i) => i.files ? [{}] : [{ result: { content: "x" } }];
    await assert.rejects(readTab(apis, 7, "text", false), /could not read/);
});

test("Send tab delivers nothing when the page is blocked during the read", async () => {
    for (const format of PAGE_FORMATS) {
        const { state, apis } = fakeChrome({ url: "https://bank.example/" });
        const delivered = [];
        const steps = {
            read: () => readTabDocument(apis, 7, format, false),
            describe: async () => {
                // Blocked after the content was read, before delivery.
                state.blocked = ["bank.example"];
                return "Bank";
            },
            checkBlocked: (sourceUrl) => assertTabNotBlocked(apis, 7, sourceUrl),
            isDestination: () => true,
            deliver: (c) => (delivered.push(c), true),
        };
        await assert.rejects(sendTabContent(steps), /blocked/, format);
        assert.deepEqual(delivered, [], format);
    }

    const { apis } = fakeChrome({ url: "https://bank.example/", blocked: ["bank.example"] });
    const delivered = [];
    await assert.rejects(sendTabContent({
        read: () => readTabDocument(apis, 7, "markdown", false),
        describe: async () => "Bank",
        checkBlocked: (sourceUrl) => assertTabNotBlocked(apis, 7, sourceUrl),
        isDestination: () => true,
        deliver: (c) => (delivered.push(c), true),
    }), /blocked/);
    assert.deepEqual(delivered, []);
});

test("Send tab checks the document it read, even after the tab moves on", async () => {
    // bank.example is read, the tab navigates to an allowed page, and then
    // bank.example is blocked: the buffered bank content is not delivered.
    const { state, apis } = fakeChrome({ url: "https://bank.example/" });
    const delivered = [];
    await assert.rejects(sendTabContent({
        read: () => readTabDocument(apis, 7, "markdown", false),
        describe: async () => {
            state.url = "https://news.example/";
            state.blocked = ["bank.example"];
            return "News";
        },
        checkBlocked: (sourceUrl) => assertTabNotBlocked(apis, 7, sourceUrl),
        isDestination: () => true,
        deliver: (c) => (delivered.push(c), true),
    }), /blocked: .*bank\.example/);
    assert.deepEqual(delivered, []);
});

test("a block saved during the read's final tab lookup stops its delivery", async () => {
    const { state, apis } = fakeChrome({ url: "https://bank.example/" });
    let reads = 0;
    const get = apis.tabs.get;
    apis.tabs.get = async (id) => {
        // The first lookup is before the read; the second is the last one.
        if (++reads === 2) {
            state.blocked = ["bank.example"];
        }
        return get(id);
    };
    await assert.rejects(readTab(apis, 7, "text", false), /blocked/);
});

test("list edits apply to the stored list, one at a time", async () => {
    assert.deepEqual(applyBlockedDomainEdit(["a.example"], { add: "b.example" }), ["a.example", "b.example"]);
    assert.deepEqual(applyBlockedDomainEdit(["a.example"], { add: "a.example" }), ["a.example"]);
    assert.deepEqual(applyBlockedDomainEdit(["a.example", "b.example"], { remove: "a.example" }), ["b.example"]);

    // A store whose saves answer late, as config.set does.
    function store(initial) {
        const s = { value: [...initial] };
        s.load = async () => [...s.value];
        s.save = async (domains) => {
            await new Promise((r) => setTimeout(r, 5));
            s.value = domains;
        };
        return s;
    }

    // Two adds before the first save answers keep both.
    const adds = store([]);
    const editAdds = createBlockedDomainEditor(adds);
    await Promise.all([editAdds({ add: "a.example" }), editAdds({ add: "b.example" })]);
    assert.deepEqual(adds.value, ["a.example", "b.example"]);

    // Two removes before the first answers restore neither.
    const removes = store(["a.example", "b.example"]);
    const editRemoves = createBlockedDomainEditor(removes);
    await Promise.all([editRemoves({ remove: "a.example" }), editRemoves({ remove: "b.example" })]);
    assert.deepEqual(removes.value, []);

    // An edit keeps entries stored by someone else since this page loaded.
    const shared = store(["a.example"]);
    const editShared = createBlockedDomainEditor(shared);
    shared.value = ["a.example", "other.example"];
    await editShared({ add: "b.example" });
    assert.deepEqual(shared.value, ["a.example", "other.example", "b.example"]);

    // A failed save does not stop later edits.
    const failing = store([]);
    const editFailing = createBlockedDomainEditor(failing);
    const save = failing.save;
    failing.save = async () => { failing.save = save; throw new Error("host gone"); };
    await assert.rejects(editFailing({ add: "a.example" }), /host gone/);
    await editFailing({ add: "b.example" });
    assert.deepEqual(failing.value, ["b.example"]);
});
