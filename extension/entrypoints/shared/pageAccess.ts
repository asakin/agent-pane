/**
 * Page reading for terminals: which Chrome tab is "the tab" for a terminal,
 * which pages Chrome will not let us read, and the messages between the
 * background and the side panel. The pure parts take no browser APIs so the
 * tests share them.
 */
import { trackMode } from "./tabLock.ts";
import type { PageFormat, PageResult } from "./pageContent.ts";
import { assertNotBlocked } from "./blockedDomains.ts";

/** Side panel menu order. Markdown is the default. */
export const SEND_FORMATS: { format: PageFormat; label: string }[] = [
    { format: "markdown", label: "Markdown" },
    { format: "text", label: "Text" },
    { format: "selection", label: "Selection" },
    { format: "info", label: "URL and title" },
    { format: "dom", label: "DOM (HTML)" },
];

export type ApprovalChoice = "tab" | "all" | "deny";

/** Background → side panel: ask the person about a read. */
export type ApproveMessage = {
    type: "agent-pane.page-approve";
    /** The host's id for this prompt, named again by a cancel. */
    id: string;
    tty: string;
    agent: string;
    tabId: number;
    title: string;
    url: string;
    /** How long the host waits for the answer. */
    timeoutMs: number;
};

/** Background → side panel: the host stopped waiting for a prompt. */
export type CancelMessage = {
    type: "agent-pane.page-cancel";
    id: string;
    tty: string;
};

/** Used when the host names no wait: its own approval timeout. */
export const DEFAULT_APPROVE_TIMEOUT_MS = 3 * 60 * 1000;

/** Background → side panel: a read happened. */
export type ReadNotice = {
    type: "agent-pane.page-read";
    tty: string;
    title: string;
    url: string;
};

/**
 * How long a prompt may wait: the host's timeout, cut short by its
 * `expiresAt` when that comes first.
 */
export function approveTimeoutMs(timeoutMs: unknown, expiresAt: unknown, now: number): number {
    let ms = typeof timeoutMs === "number" && Number.isFinite(timeoutMs) && timeoutMs > 0 ? timeoutMs : DEFAULT_APPROVE_TIMEOUT_MS;
    if (typeof expiresAt === "number" && Number.isFinite(expiresAt)) {
        ms = Math.min(ms, expiresAt - now);
    }
    return ms;
}

export function isApproveMessage(msg: unknown): msg is ApproveMessage {
    const m = msg as ApproveMessage;
    return !!m && m.type === "agent-pane.page-approve" && typeof m.tty === "string" && Number.isInteger(m.tabId);
}

export function isCancelMessage(msg: unknown): msg is CancelMessage {
    const m = msg as CancelMessage;
    return !!m && m.type === "agent-pane.page-cancel" && typeof m.tty === "string" && typeof m.id === "string" && m.id !== "";
}

type Timers = {
    setTimeout(fn: () => void, ms: number): unknown;
    clearTimeout(id: unknown): void;
};

/**
 * Approval prompts waiting for the person, oldest first. Only the first is
 * shown. A prompt leaves the queue when it is answered, when the host cancels
 * it, or when the host's wait for it runs out, whether or not it was shown
 * yet, so a prompt nobody waits for never holds up a live one or takes its
 * answer. A prompt that leaves unanswered resolves as deny; the host has
 * already given up on it.
 */
export function createApprovalQueue(onChange: () => void, timers: Timers = globalThis) {
    type Entry = { ask: ApproveMessage; resolve: (choice: ApprovalChoice) => void; timer: unknown };
    let queue: Entry[] = [];

    const settle = (entry: Entry, choice: ApprovalChoice) => {
        const wasFirst = queue[0] === entry;
        queue = queue.filter((e) => e !== entry);
        timers.clearTimeout(entry.timer);
        entry.resolve(choice);
        if (wasFirst) {
            onChange();
        }
    };

    return {
        /** The prompt to show, if any. */
        current(): ApproveMessage | undefined {
            return queue[0]?.ask;
        },
        ask(ask: ApproveMessage): Promise<ApprovalChoice> {
            return new Promise((resolve) => {
                // A prompt whose time has run out leaves at once.
                const ms = Number.isFinite(ask.timeoutMs) ? Math.max(0, ask.timeoutMs) : DEFAULT_APPROVE_TIMEOUT_MS;
                const entry: Entry = { ask, resolve, timer: undefined };
                entry.timer = timers.setTimeout(() => settle(entry, "deny"), ms);
                queue.push(entry);
                if (queue.length === 1) {
                    onChange();
                }
            });
        },
        /** Answers the shown prompt, if it is still the one with this id. */
        answer(id: string, choice: ApprovalChoice) {
            const first = queue[0];
            if (first && first.ask.id === id) {
                settle(first, choice);
            }
        },
        cancel(tty: string, id: string) {
            const entry = queue.find((e) => e.ask.id === id && e.ask.tty === tty);
            if (entry) {
                settle(entry, "deny");
            }
        },
    };
}

export function isReadNotice(msg: unknown): msg is ReadNotice {
    const m = msg as ReadNotice;
    return !!m && m.type === "agent-pane.page-read" && typeof m.tty === "string";
}

export function approvalChoice(answer: unknown): ApprovalChoice {
    const choice = (answer as { choice?: unknown } | undefined)?.choice;
    return choice === "tab" || choice === "all" ? choice : "deny";
}

/** Why Chrome will not let an extension script this URL, or null. */
export function unreadableReason(url: string): string | null {
    let u: URL;
    try {
        u = new URL(url);
    } catch {
        return url ? `Chrome does not let extensions read ${url}` : "the tab has no page loaded";
    }
    const host = u.hostname;
    if (host === "chromewebstore.google.com" || (host === "chrome.google.com" && u.pathname.startsWith("/webstore"))) {
        return "Chrome does not let extensions read the Chrome Web Store";
    }
    switch (u.protocol) {
        case "http:":
        case "https:":
        case "file:":
            return null;
        case "chrome-extension:":
            return "Chrome does not let extensions read other extensions' pages";
        case "chrome:":
        case "chrome-untrusted:":
        case "devtools:":
        case "edge:":
        case "about:":
        case "view-source:":
            return `Chrome does not let extensions read ${u.protocol}// pages`;
        default:
            return `Chrome does not let extensions read ${u.protocol} pages`;
    }
}

/** A failed executeScript, put the way a person reads it. */
export function scriptingFailure(url: string, err: unknown): string {
    const reason = unreadableReason(url);
    if (reason) {
        return reason;
    }
    const message = err instanceof Error ? err.message : String(err ?? "");
    if (/pdf/i.test(url) || /mime ?handler|PDF/i.test(message)) {
        return "Chrome's built-in PDF viewer cannot be read by extensions";
    }
    if (/file:\/\//.test(url) || /file/i.test(message) && /access/i.test(message)) {
        return "turn on \"Allow access to file URLs\" for Agent Pane in chrome://extensions to read local files";
    }
    if (/error page/i.test(message)) {
        return "the tab shows an error page";
    }
    return message ? `could not read the tab: ${message}` : "could not read the tab";
}

type StoredTab = { id?: unknown; lockedTabId?: number; lockedTabClosed?: boolean };

export type TerminalPlace =
    | { found: false }
    | { found: true; windowId: number; mode: "following" }
    | { found: true; windowId: number; mode: "locked"; tabId: number }
    | { found: true; windowId: number; mode: "closed" };

/** Finds the side-panel window holding tty in storage.session window states. */
export function findTerminal(stored: Record<string, unknown>, tty: string): TerminalPlace {
    for (const [key, value] of Object.entries(stored)) {
        const match = /^agent-pane\.window\.(\d+)$/.exec(key);
        const tabs = (value as { tabs?: unknown } | undefined)?.tabs;
        if (!match || !Array.isArray(tabs)) {
            continue;
        }
        const tab = (tabs as StoredTab[]).find((t) => t && typeof t === "object" && t.id === tty);
        if (!tab) {
            continue;
        }
        const windowId = Number(match[1]);
        const mode = trackMode(tab);
        if (mode === "locked") {
            return { found: true, windowId, mode, tabId: tab.lockedTabId! };
        }
        return { found: true, windowId, mode };
    }
    return { found: false };
}

type ChromeTab = { id?: number; title?: string; url?: string; pendingUrl?: string };

export type PageApis = {
    /** The Blocked domains list as it is now, read again at every call. */
    blockedDomains(): Promise<string[]>;
    storageSession: { get(keys: null): Promise<Record<string, unknown>> };
    tabs: {
        get(tabId: number): Promise<ChromeTab>;
        query(q: { active: true; windowId?: number; lastFocusedWindow?: boolean }): Promise<ChromeTab[]>;
    };
    scripting: {
        executeScript(injection: {
            target: { tabId: number; frameIds?: number[] };
            files?: string[];
            func?: (...args: any[]) => unknown;
            args?: unknown[];
        }): Promise<{ result?: unknown }[]>;
    };
};

export type ResolvedTab = { tabId: number; title: string; url: string };

/**
 * "The tab" for a terminal: its lock, else the active tab of its window. A
 * terminal outside the side panel (its own window) uses the last focused
 * window's active tab.
 */
export async function resolveTab(apis: PageApis, tty: string): Promise<ResolvedTab> {
    const place = findTerminal(await apis.storageSession.get(null), tty);
    let tab: ChromeTab | undefined;
    if (place.found && place.mode === "closed") {
        throw new Error("the Chrome tab locked to this terminal was closed; click the status bar to unlock it");
    }
    if (place.found && place.mode === "locked") {
        try {
            tab = await apis.tabs.get(place.tabId);
        } catch {
            throw new Error("the Chrome tab locked to this terminal was closed; click the status bar to unlock it");
        }
    } else {
        const query = place.found
            ? { active: true as const, windowId: place.windowId }
            : { active: true as const, lastFocusedWindow: true };
        [tab] = await apis.tabs.query(query);
    }
    if (!tab || tab.id === undefined) {
        throw new Error("no Chrome tab is focused in this terminal's window");
    }
    // A blocked page, or a navigation to one, is refused before anyone is
    // asked about it.
    assertNotBlocked([tab.url, tab.pendingUrl], await apis.blockedDomains());
    // The committed document, which is what a read gets; a navigation still
    // pending has not replaced it yet. A prompted read checks it is still
    // this document before anything is delivered.
    return { tabId: tab.id, title: tab.title ?? "", url: tab.url || tab.pendingUrl || "" };
}

/**
 * Refuses a tab whose committed page, or the page it is navigating to, is
 * blocked by the current list.
 */
export async function assertTabNotBlocked(apis: Pick<PageApis, "tabs" | "blockedDomains">, tabId: number): Promise<void> {
    const tab = await apis.tabs.get(tabId).catch(() => {
        throw new Error("the tab was closed");
    });
    assertNotBlocked([tab.url, tab.pendingUrl], await apis.blockedDomains());
}

/**
 * Reads one tab: main frame, isolated world, result returned once. The
 * Blocked domains list is checked before the reader runs, by the reader
 * against the document it reads, and again, as it is then, against that
 * document and the tab after the read, so a navigation, a redirect or a block
 * added while the read runs never delivers a blocked page.
 */
export async function readTab(apis: Pick<PageApis, "tabs" | "scripting" | "blockedDomains">, tabId: number, format: string, full: boolean, expectUrl?: string): Promise<string> {
    const tab = await apis.tabs.get(tabId).catch(() => {
        throw new Error("the tab was closed");
    });
    const url = tab.url || tab.pendingUrl || "";
    const reason = unreadableReason(url);
    if (reason) {
        throw new Error(reason);
    }
    const blocked = await apis.blockedDomains();
    assertNotBlocked([tab.url, tab.pendingUrl], blocked);
    let results: { result?: unknown }[];
    try {
        const target = { tabId, frameIds: [0] };
        await apis.scripting.executeScript({ target, files: ["/page-reader.js"] });
        results = await apis.scripting.executeScript({
            target,
            func: (f: string, all: boolean, expect: string | null, block: string[]) =>
                (globalThis as unknown as { __agentPaneRead: (f: string, all: boolean, expect: string | undefined, block: string[]) => unknown })
                    .__agentPaneRead(f, all, expect ?? undefined, block),
            // executeScript arguments must be JSON: null, not undefined.
            args: [format, full, expectUrl ?? null, blocked],
        });
    } catch (err) {
        throw new Error(scriptingFailure(url, err));
    }
    const result = results[0]?.result as (PageResult & { url?: unknown }) | undefined;
    if (!result) {
        throw new Error(scriptingFailure(url, undefined));
    }
    if ("error" in result) {
        throw new Error(result.error);
    }
    // The reader names the document it read; without it nothing is known
    // to be safe to deliver.
    if (typeof result.url !== "string") {
        throw new Error(scriptingFailure(url, undefined));
    }
    const now = await apis.blockedDomains();
    const after = await apis.tabs.get(tabId).catch(() => undefined);
    assertNotBlocked([result.url, after?.url, after?.pendingUrl], now);
    return typeof result.content === "string" ? result.content : "";
}

/**
 * The bytes Send tab types into the terminal. Page text is untrusted, so
 * control characters (including ESC, which could end the bracketed paste
 * early) are removed, and trailing newlines are dropped so nothing presses
 * Enter. Without bracketed paste every newline would run a line, so
 * multi-line text is refused.
 */
export function pasteBytes(text: string, bracketed: boolean): string {
    const clean = text
        .replace(/\r\n?/g, "\n")
        .replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/g, "")
        .replace(/\n+$/, "");
    if (!bracketed) {
        if (clean.includes("\n")) {
            throw new Error("this terminal's program has not turned on bracketed paste, so pasting several lines would run them");
        }
        return clean;
    }
    return `\x1b[200~${clean.replace(/\n/g, "\r")}\x1b[201~`;
}

/**
 * Send tab's steps after the click. The Blocked domains list is checked
 * again after the page is described, and the destination after the last
 * await, immediately before delivery, so a block added while the page is
 * read never delivers it, and switching terminals never pastes it into the
 * wrong one. Delivery reports whether
 * the attached session took the input: while a reattached socket replays
 * scrollback, input is dropped. Returns the tab's description, or null when
 * nothing was delivered.
 */
export async function sendTabContent(steps: {
    read(): Promise<string>;
    describe(): Promise<string>;
    /** Throws when the page is blocked now. */
    checkBlocked(): Promise<void>;
    isDestination(): boolean;
    deliver(content: string): boolean;
}): Promise<string | null> {
    const content = await steps.read();
    const description = await steps.describe();
    await steps.checkBlocked();
    if (!steps.isDestination()) {
        return null;
    }
    return steps.deliver(content) ? description : null;
}
