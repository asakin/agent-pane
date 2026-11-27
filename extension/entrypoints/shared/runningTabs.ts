import type { ListedSpace, ListedTTY, RequestLayoutTTY } from "./rpc.ts";

/**
 * Durable (preview) pane tabs outlive the side panel that showed them: a
 * Chrome window closing, Chrome quitting, the extension reloading or the host
 * restarting leaves their programs running. A side panel takes back the ones
 * last shown in its own window, and offers the rest as "Running pane tabs"
 * it can open or end.
 */
export type RunningSplit = {
    /** Last shown in this window: the panel shows them again. */
    restore: ListedTTY[];
    /** Shown in no open window: offered in the Running pane tabs list. */
    offer: ListedTTY[];
};

/**
 * Sorts tty.list's sessions for a side panel. mine is this window's name,
 * held the sessions the panel already has, and openWindows the names of the
 * other Chrome windows open now, whose own panels hold their pane tabs.
 * Built-in sessions end with their panel's host, so only Durable ones count.
 */
export function splitRunning(
    sessions: ListedTTY[],
    mine: string,
    held: Set<string>,
    openWindows: Set<string>,
): RunningSplit {
    const restore: ListedTTY[] = [];
    const offer: ListedTTY[] = [];
    for (const s of sessions) {
        if (!s || typeof s.id !== "string" || !s.id || s.durable !== true || held.has(s.id)) {
            continue;
        }
        const window = typeof s.window === "string" ? s.window : "";
        if (window && window === mine) {
            restore.push(s);
        } else if (!window || !openWindows.has(window)) {
            offer.push(s);
        }
    }
    return { restore, offer };
}

/**
 * A space the Running pane tabs list offers: the pane tabs one Chrome window
 * showed, in their order, as the host's layout document has them. Pane tabs
 * with no space in the document are each offered alone.
 */
export type OfferedSpace = {
    /** The space's id; absent for a pane tab with no space. */
    id?: string;
    name?: string;
    /** The window it was last shown in, as windowName makes it. */
    window?: string;
    tabs: ListedTTY[];
};

function listedSpace(s: ListedTTY): ListedSpace | undefined {
    const sp = s.space;
    if (!sp || typeof sp !== "object" || typeof sp.id !== "string" || !sp.id || !Number.isInteger(sp.position)) {
        return undefined;
    }
    return sp;
}

/** Orders sessions by their position in their spaces. */
export function byPosition(sessions: ListedTTY[]): ListedTTY[] {
    return [...sessions].sort((a, b) => (listedSpace(a)?.position ?? Infinity) - (listedSpace(b)?.position ?? Infinity));
}

/** Groups offered sessions into the spaces they belong to, each in order. */
export function groupSpaces(offer: ListedTTY[]): OfferedSpace[] {
    const spaces: OfferedSpace[] = [];
    const byId = new Map<string, OfferedSpace>();
    for (const s of offer) {
        const sp = listedSpace(s);
        if (!sp) {
            spaces.push({ tabs: [s] });
            continue;
        }
        let group = byId.get(sp.id);
        if (!group) {
            group = {
                id: sp.id,
                ...(typeof sp.name === "string" && sp.name ? { name: sp.name } : {}),
                ...(typeof sp.window === "string" && sp.window ? { window: sp.window } : {}),
                tabs: [],
            };
            byId.set(sp.id, group);
            spaces.push(group);
        }
        group.tabs.push(s);
    }
    for (const group of spaces) {
        group.tabs = byPosition(group.tabs);
    }
    return spaces;
}

/** The run id in a window name (`<windowId>@<runId>`), "" without one. */
export function windowRun(window: string | undefined): string {
    const at = typeof window === "string" ? window.lastIndexOf("@") : -1;
    return at >= 0 ? window!.slice(at + 1) : "";
}

/**
 * The pane tabs a window opens when it takes a space: in order, the one the
 * space showed last active. A lock names a Chrome tab of one browser run, so
 * it carries only into a window of the same run; approvals never do.
 */
export function openSpace(space: OfferedSpace, runId: string): { tabs: SpaceTab[]; activeId: string } {
    const tabs = space.tabs.map((s) => listedTab(s, runId));
    const active = space.tabs.find((s) => listedSpace(s)?.active === true) ?? space.tabs[0];
    return { tabs, activeId: active?.id ?? "" };
}

/**
 * A pane tab for a running session the panel did not start. Its lock
 * carries when the window its space was last shown in is of this browser
 * run; approvals never do.
 */
export function listedTab(s: ListedTTY, runId: string): SpaceTab {
    const sp = listedSpace(s);
    const locked = sp?.lockedTab;
    const sameRun = !!runId && windowRun(sp?.window) === runId;
    return {
        id: s.id,
        app: typeof s.agent === "string" ? s.agent : "",
        ...(typeof s.pane === "string" && s.pane ? { pane: s.pane } : {}),
        ...(sameRun && Number.isInteger(locked) ? { lockedTabId: locked } : {}),
    };
}

export type SpaceTab = { id: string; app: string; pane?: string; lockedTabId?: number };

/** What the Running pane tabs list calls a space. */
export function spaceName(space: OfferedSpace): string {
    if (space.name) {
        return space.name;
    }
    const names = space.tabs.map(runningName);
    return names.length <= 2 ? names.join(", ") : `${names[0]} and ${names.length - 1} more`;
}

/**
 * tty.layout's params for what a window shows: its pane tabs in order, the
 * active one and their locks. A lock on a Chrome tab that closed is not one.
 */
export function layoutParams(
    window: string,
    tabs: { id: string; lockedTabId?: number; lockedTabClosed?: boolean }[],
    activeId: string,
    space?: string,
): NonNullable<RequestLayoutTTY["params"]> {
    return {
        window,
        tabs: tabs.map((tab) => tab.lockedTabId !== undefined && !tab.lockedTabClosed
            ? { id: tab.id, lockedTab: tab.lockedTabId }
            : { id: tab.id }),
        active: activeId,
        ...(space ? { space } : {}),
    };
}

export type LayoutReport<S> = { window: string; state: S; space?: string };

/**
 * Sends a window's layout reports one at a time, each after the host
 * answered the one before, so the host applies them in the order they were
 * made. Reports made while one is in flight collapse into the newest, which
 * keeps the space an older one took.
 */
export function serialReporter<S>(send: (report: LayoutReport<S>) => Promise<void>): (report: LayoutReport<S>) => void {
    let pending: LayoutReport<S> | undefined;
    let busy = false;
    const drain = async () => {
        busy = true;
        while (pending) {
            const next = pending;
            pending = undefined;
            try {
                await send(next);
            } catch {
                // The next report carries everything since.
            }
        }
        busy = false;
    };
    return (report) => {
        const space = report.space ?? pending?.space;
        pending = { ...report, ...(space ? { space } : {}) };
        if (!busy) {
            void drain();
        }
    };
}

/** A fresh browser-run id: 12 hex digits. */
export function newRunId(): string {
    const bytes = new Uint8Array(6);
    crypto.getRandomValues(bytes);
    return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The host's name for a Chrome window of this browser run: `<windowId>@<runId>`. */
export function windowName(windowId: number, runId: string): string {
    return `${windowId}@${runId}`;
}

/** What the Running pane tabs list calls a session. */
export function runningName(s: ListedTTY): string {
    const title = typeof s.title === "string" ? s.title.trim() : "";
    const label = typeof s.label === "string" && s.label ? s.label : "Terminal";
    return title && title !== label ? `${label}: ${title}` : label;
}

export type RunningTabsHandlers = {
    /** Show the session as a pane tab in this window. */
    onOpen: (s: ListedTTY) => void;
    /** Show every pane tab of a space in this window, in its order. */
    onOpenSpace: (space: OfferedSpace) => void;
    /** End the session and close its pane tab. */
    onEnd: (s: ListedTTY) => void;
    /** Called before the list opens, to bring it up to date. */
    onRefresh?: () => Promise<void>;
};

export type RunningTabsController = {
    set: (offer: ListedTTY[]) => void;
};

/**
 * The strip's Running pane tabs button: hidden while nothing is offered,
 * otherwise a count that opens the list, each entry with Open and End.
 */
export function mountRunningTabs(host: HTMLElement, handlers: RunningTabsHandlers): RunningTabsController {
    const doc = host.ownerDocument;
    host.classList.add("at-running");
    host.replaceChildren();
    host.hidden = true;

    let offered: ListedTTY[] = [];
    let menu: HTMLElement | null = null;

    const button = doc.createElement("button");
    button.type = "button";
    button.className = "at-running__button";
    button.setAttribute("aria-haspopup", "menu");
    button.setAttribute("aria-expanded", "false");
    host.appendChild(button);

    const closeMenu = () => {
        if (!menu) {
            return;
        }
        menu.remove();
        menu = null;
        button.setAttribute("aria-expanded", "false");
        doc.removeEventListener("pointerdown", onDocPointer, true);
        doc.removeEventListener("keydown", onKey, true);
    };

    const onDocPointer = (e: Event) => {
        const t = e.target as Node;
        if (host.contains(t) || menu?.contains(t)) {
            return;
        }
        closeMenu();
    };

    const onKey = (e: KeyboardEvent) => {
        if (e.key === "Escape") {
            e.preventDefault();
            closeMenu();
        }
    };

    const renderMenu = () => {
        if (!menu) {
            return;
        }
        const heading = doc.createElement("div");
        heading.className = "at-running__heading";
        heading.textContent = "Running pane tabs";
        const rows: HTMLElement[] = [heading];
        for (const space of groupSpaces(offered)) {
            if (space.tabs.length > 1) {
                const row = doc.createElement("div");
                row.className = "at-running__space";
                row.dataset.spaceId = space.id ?? "";
                const open = doc.createElement("button");
                open.type = "button";
                open.className = "at-running__open-space";
                open.setAttribute("role", "menuitem");
                open.textContent = `Open all ${space.tabs.length}: ${spaceName(space)}`;
                open.title = "Open these pane tabs in this window, in their order";
                open.addEventListener("click", () => {
                    closeMenu();
                    handlers.onOpenSpace(space);
                });
                row.append(open);
                rows.push(row);
            }
            rows.push(...space.tabs.map(itemRow));
        }
        const hint = doc.createElement("div");
        hint.className = "at-running__hint";
        hint.textContent = "Still running after their side panel closed";
        rows.push(hint);
        menu.replaceChildren(...rows);
    };

    const itemRow = (s: ListedTTY): HTMLElement => {
        const row = doc.createElement("div");
        row.className = "at-running__item";
        row.dataset.ttyId = s.id;

        const open = doc.createElement("button");
        open.type = "button";
        open.className = "at-running__open";
        open.setAttribute("role", "menuitem");
        open.textContent = runningName(s);
        open.title = "Open in this window";
        open.addEventListener("click", () => {
            closeMenu();
            handlers.onOpen(s);
        });

        const end = doc.createElement("button");
        end.type = "button";
        end.className = "at-running__end";
        end.textContent = "End";
        end.title = "End this pane tab's program";
        end.addEventListener("click", () => {
            closeMenu();
            handlers.onEnd(s);
        });

        row.append(open, end);
        return row;
    };

    const openMenu = async () => {
        if (menu) {
            closeMenu();
            return;
        }
        await handlers.onRefresh?.();
        if (offered.length === 0) {
            return;
        }
        menu = doc.createElement("div");
        menu.className = "at-running__menu";
        menu.setAttribute("role", "menu");
        renderMenu();
        doc.body.appendChild(menu);
        const r = button.getBoundingClientRect?.();
        if (r) {
            menu.style.top = `${r.bottom + 4}px`;
            menu.style.left = `${Math.max(8, r.left)}px`;
        }
        button.setAttribute("aria-expanded", "true");
        doc.addEventListener("pointerdown", onDocPointer, true);
        doc.addEventListener("keydown", onKey, true);
    };

    button.addEventListener("click", () => void openMenu());

    return {
        set(offer: ListedTTY[]) {
            offered = offer;
            host.hidden = offer.length === 0;
            const text = `${offer.length} running`;
            if (button.textContent !== text) {
                button.textContent = text;
            }
            const tip = offer.length === 1
                ? "1 pane tab is still running outside a side panel"
                : `${offer.length} pane tabs are still running outside a side panel`;
            button.title = tip;
            button.setAttribute("aria-label", tip);
            if (offer.length === 0) {
                closeMenu();
            } else {
                renderMenu();
            }
        },
    };
}
