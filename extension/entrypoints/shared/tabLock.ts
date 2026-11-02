/**
 * Which Chrome tab each agent tab tracks. Pure helpers: no browser APIs, so
 * the side panel and the tests share the same state transitions.
 */

/** Lock fields carried on each agent tab in WindowTabState. */
export type TabLock = {
    /** Chrome tab id this agent tab is locked to; absent while following. */
    lockedTabId?: number;
    /** The locked Chrome tab was closed. Sticky until the user unlocks. */
    lockedTabClosed?: boolean;
};

type LockState = {
    tabs: (TabLock & { id: string })[];
    activeId: string;
};

type LockTab<S extends LockState> = S["tabs"][number];

export type ChromeTabLike = {
    id?: number;
    windowId?: number;
    url?: string;
    pendingUrl?: string;
    title?: string;
};

export type TrackMode = "following" | "locked" | "closed";

export type StatusView = {
    mode: TrackMode;
    text: string;
    tooltip: string;
};

export function isTabLock(value: TabLock): boolean {
    if (value.lockedTabId !== undefined && !Number.isInteger(value.lockedTabId)) {
        return false;
    }
    if (value.lockedTabClosed !== undefined && typeof value.lockedTabClosed !== "boolean") {
        return false;
    }
    return true;
}

export function trackMode(tab: TabLock | undefined): TrackMode {
    if (!tab || tab.lockedTabId === undefined) {
        return "following";
    }
    return tab.lockedTabClosed ? "closed" : "locked";
}

/** Chrome tab id the agent tab shows: its lock, else the window's active tab. Undefined when closed. */
export function trackedTabId(tab: TabLock | undefined, activeChromeTabId: number | undefined): number | undefined {
    switch (trackMode(tab)) {
        case "closed":
            return undefined;
        case "locked":
            return tab!.lockedTabId;
        default:
            return activeChromeTabId;
    }
}

function withoutLock<T extends TabLock>(tab: T): T {
    const { lockedTabId: _id, lockedTabClosed: _closed, ...rest } = tab;
    return rest as T;
}

function mapTabs<S extends LockState>(state: S, fn: (tab: LockTab<S>) => LockTab<S>): S {
    let changed = false;
    const tabs = state.tabs.map((tab) => {
        const next = fn(tab);
        changed ||= next !== tab;
        return next;
    });
    return changed ? { ...state, tabs } : state;
}

/**
 * Click on the status bar: lock a following agent tab to the Chrome tab it
 * shows; unlock a locked or closed one. Other agent tabs are untouched.
 */
export function toggleLock<S extends LockState>(state: S, agentId: string, shownChromeTabId: number | undefined): S {
    return mapTabs(state, (tab) => {
        if (tab.id !== agentId) {
            return tab;
        }
        if (trackMode(tab) !== "following") {
            return withoutLock(tab);
        }
        if (shownChromeTabId === undefined) {
            return tab;
        }
        return { ...withoutLock(tab), lockedTabId: shownChromeTabId };
    });
}

/** tabs.onRemoved: every agent tab locked to chromeTabId enters the sticky closed state. */
export function markLockedTabClosed<S extends LockState>(state: S, chromeTabId: number): S {
    return mapTabs(state, (tab) =>
        tab.lockedTabId === chromeTabId && !tab.lockedTabClosed ? { ...tab, lockedTabClosed: true } : tab);
}

/** tabs.onReplaced: move locks from removedTabId to addedTabId. */
export function replaceLockedTab<S extends LockState>(state: S, addedTabId: number, removedTabId: number): S {
    return mapTabs(state, (tab) =>
        tab.lockedTabId === removedTabId && !tab.lockedTabClosed ? { ...tab, lockedTabId: addedTabId } : tab);
}

/** After a side-panel reload: locks whose Chrome tab no longer exists become closed. */
export async function closeMissingLocks<S extends LockState>(state: S, tabExists: (chromeTabId: number) => Promise<boolean>): Promise<S> {
    const ids = new Set<number>();
    for (const tab of state.tabs) {
        if (trackMode(tab) === "locked") {
            ids.add(tab.lockedTabId!);
        }
    }
    let next = state;
    for (const id of ids) {
        if (!(await tabExists(id))) {
            next = markLockedTabClosed(next, id);
        }
    }
    return next;
}

/**
 * Whether a tabs.onActivated / onUpdated event for chromeTab concerns an agent
 * tab. Following tabs only react inside the side panel's window; locked tabs
 * follow their tab id into any window.
 */
export function concernsAgentTab(tab: TabLock | undefined, panelWindowId: number, activeChromeTabId: number | undefined, chromeTab: ChromeTabLike): boolean {
    switch (trackMode(tab)) {
        case "closed":
            return false;
        case "locked":
            return chromeTab.id === tab!.lockedTabId;
        default:
            return chromeTab.windowId === panelWindowId && chromeTab.id === activeChromeTabId;
    }
}

const NEW_TAB_URLS = new Set([
    "chrome://newtab/",
    "chrome://new-tab-page/",
    "chrome-search://local-ntp/local-ntp.html",
    "edge://newtab/",
    "about:newtab",
    "about:home",
]);

/** Prefer the URL a tab is navigating to over the one it is leaving. */
export function chromeTabUrl(tab: ChromeTabLike): string {
    return tab.pendingUrl || tab.url || "";
}

/** Short status-bar text for a URL. */
export function describeUrl(url: string): string {
    if (!url) {
        return "No page";
    }
    if (NEW_TAB_URLS.has(url)) {
        return "New Tab";
    }
    if (url.startsWith("https://")) {
        return url.slice("https://".length);
    }
    return url;
}

export function statusView(tab: TabLock | undefined, chromeTab: ChromeTabLike | undefined): StatusView {
    const mode = trackMode(tab);
    if (mode === "closed") {
        return {
            mode,
            text: "Tab closed",
            tooltip: "The locked Chrome tab was closed. Click to follow the active tab.",
        };
    }
    const url = chromeTab ? chromeTabUrl(chromeTab) : "";
    const text = chromeTab ? describeUrl(url) : "No tab";
    const details = [chromeTab?.title?.trim(), url].filter((line) => !!line && line !== text);
    const hint = mode === "locked"
        ? "Locked to this Chrome tab. Click to follow the active tab."
        : "Following the active Chrome tab. Click to lock to this tab.";
    return { mode, text, tooltip: [text, ...details, hint].join("\n") };
}
