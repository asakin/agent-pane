import type { ListedTTY } from "./rpc.ts";

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
        for (const s of offered) {
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
            rows.push(row);
        }
        const hint = doc.createElement("div");
        hint.className = "at-running__hint";
        hint.textContent = "Still running after their side panel closed";
        rows.push(hint);
        menu.replaceChildren(...rows);
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
