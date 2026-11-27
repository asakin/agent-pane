import type { WindowTabState } from "./windowTabs.ts";
import { type PaneStatus, statusMark } from "./paneStatus.ts";

export type TabStripHandlers = {
    onSelect: (id: string) => void;
    onClose: (id: string) => void;
    /** Moves the pane tab to slot, a place between the tabs as they are now (0 is first, tabs.length last). */
    onMove?: (id: string, slot: number) => void;
};

/**
 * The state with the pane tab moved to slot, counted between the tabs as
 * they are before the move. The active pane tab stays active. Returns state
 * itself when nothing moves.
 */
export function moveTab(state: WindowTabState, id: string, slot: number): WindowTabState {
    const from = state.tabs.findIndex((tab) => tab.id === id);
    if (from < 0) {
        return state;
    }
    const clamped = Math.max(0, Math.min(slot, state.tabs.length));
    const to = clamped > from ? clamped - 1 : clamped;
    if (to === from) {
        return state;
    }
    const tabs = state.tabs.slice();
    tabs.splice(to, 0, ...tabs.splice(from, 1));
    return { tabs, activeId: state.activeId };
}

/** The slot a drop at x lands in, given each tab's left edge and width in order. */
export function dropSlot(boxes: { left: number; width: number }[], x: number): number {
    const i = boxes.findIndex((box) => x < box.left + box.width / 2);
    return i < 0 ? boxes.length : i;
}

const listHandlers = new WeakMap<HTMLElement, TabStripHandlers>();
// The pane tab being dragged in each list, so a drag from elsewhere is ignored.
const dragging = new WeakMap<HTMLElement, string>();

function tabButtons(list: HTMLElement): HTMLElement[] {
    return (Array.from(list.children) as HTMLElement[]).filter((child) => child.dataset.tabId);
}

function clearDropMark(list: HTMLElement) {
    for (const tab of tabButtons(list)) {
        delete tab.dataset.drop;
    }
}

function slotAt(list: HTMLElement, x: number): number {
    return dropSlot(tabButtons(list).map((tab) => tab.getBoundingClientRect()), x);
}

/** Listens on the list once for drags of its pane tabs. */
function watchDrops(list: HTMLElement) {
    list.addEventListener("dragover", (e) => {
        if (!dragging.has(list)) {
            return;
        }
        e.preventDefault();
        if (e.dataTransfer) {
            e.dataTransfer.dropEffect = "move";
        }
        const tabs = tabButtons(list);
        const slot = slotAt(list, e.clientX);
        tabs.forEach((tab, i) => {
            const mark = i === slot ? "before" : (slot === tabs.length && i === tabs.length - 1 ? "after" : undefined);
            if (mark) {
                tab.dataset.drop = mark;
            } else {
                delete tab.dataset.drop;
            }
        });
    });
    list.addEventListener("dragleave", (e) => {
        if (!e.relatedTarget || !list.contains(e.relatedTarget as Node)) {
            clearDropMark(list);
        }
    });
    list.addEventListener("drop", (e) => {
        const id = dragging.get(list);
        if (id === undefined) {
            return;
        }
        e.preventDefault();
        dragging.delete(list);
        clearDropMark(list);
        listHandlers.get(list)?.onMove?.(id, slotAt(list, e.clientX));
    });
}

/**
 * Brings the tab list in line with state. Existing pane tab buttons are
 * updated in place rather than rebuilt: busy agents change their title many
 * times a second, and a button replaced between mousedown and mouseup never
 * gets its click.
 */
export function renderTabList(
    list: HTMLElement,
    state: WindowTabState,
    titles: Map<string, string>,
    handlers: TabStripHandlers,
    statuses: Map<string, PaneStatus> = new Map(),
) {
    if (!listHandlers.has(list)) {
        watchDrops(list);
    }
    listHandlers.set(list, handlers);
    const existing = new Map<string, HTMLElement>();
    for (const child of Array.from(list.children)) {
        const id = (child as HTMLElement).dataset.tabId;
        if (id) {
            existing.set(id, child as HTMLElement);
        }
    }

    let previous: Element | null = null;
    for (const entry of state.tabs) {
        const id = entry.id;
        const tab = existing.get(id) ?? createTabButton(list, id, handlers);
        existing.delete(id);

        const status = statuses.get(id);
        const mark = statusMark(status);
        // A pane tab out of sight that waits on the person draws attention.
        const className = "at-tabs__tab"
            + (id === state.activeId ? " at-tabs__tab--active" : "")
            + (status?.state === "blocked" && id !== state.activeId ? " at-tabs__tab--attention" : "");
        if (tab.className !== className) {
            tab.className = className;
        }
        const dot = tab.querySelector(".at-tabs__status") as HTMLElement | null;
        if (dot) {
            const dotClass = mark?.className ?? "at-tabs__status";
            if (dot.className !== dotClass) {
                dot.className = dotClass;
            }
            dot.hidden = !mark;
            const dotText = mark?.text ?? "";
            if (dot.title !== dotText) {
                dot.title = dotText;
                if (dotText) {
                    dot.setAttribute("aria-label", dotText);
                } else {
                    dot.removeAttribute("aria-label");
                }
            }
        }
        const named = titles.get(id) || id;
        const title = mark ? `${named}: ${mark.text}` : named;
        if (tab.title !== title) {
            tab.title = title;
        }
        const label = tab.querySelector(".at-tabs__label");
        const text = shortTabLabel(id, titles.get(id));
        if (label && label.textContent !== text) {
            label.textContent = text;
        }

        const expected: Element | null = previous ? previous.nextElementSibling : list.firstElementChild;
        if (expected !== tab) {
            list.insertBefore(tab, expected);
        }
        previous = tab;
    }

    for (const stale of existing.values()) {
        stale.remove();
    }
    // Anything else left after the last pane tab is not ours.
    let rest = previous ? previous.nextElementSibling : list.firstElementChild;
    while (rest) {
        const next = rest.nextElementSibling;
        rest.remove();
        rest = next;
    }
}

function createTabButton(list: HTMLElement, id: string, handlers: TabStripHandlers): HTMLElement {
    const doc = list.ownerDocument;
    const tab = doc.createElement("button");
    tab.type = "button";
    tab.dataset.tabId = id;
    tab.draggable = true;
    tab.setAttribute("aria-keyshortcuts", "Control+Shift+ArrowLeft Control+Shift+ArrowRight");

    const status = doc.createElement("span");
    status.className = "at-tabs__status";
    status.setAttribute("role", "img");
    status.hidden = true;
    tab.appendChild(status);

    const label = doc.createElement("span");
    label.className = "at-tabs__label";
    tab.appendChild(label);

    const close = doc.createElement("span");
    close.className = "at-tabs__close";
    close.textContent = "×";
    close.title = "Close terminal";
    close.addEventListener("click", (e) => {
        e.stopPropagation();
        handlers.onClose(id);
    });
    tab.appendChild(close);

    tab.addEventListener("click", () => handlers.onSelect(id));

    tab.addEventListener("dragstart", (e) => {
        dragging.set(list, id);
        if (e.dataTransfer) {
            e.dataTransfer.effectAllowed = "move";
            e.dataTransfer.setData("text/plain", id);
        }
    });
    tab.addEventListener("dragend", () => {
        dragging.delete(list);
        clearDropMark(list);
    });

    // Ctrl+Shift+Left and Right move the focused pane tab one place.
    tab.addEventListener("keydown", (e) => {
        if (!e.ctrlKey || !e.shiftKey || e.altKey || e.metaKey || (e.key !== "ArrowLeft" && e.key !== "ArrowRight")) {
            return;
        }
        e.preventDefault();
        const from = tabButtons(list).indexOf(tab);
        listHandlers.get(list)?.onMove?.(id, e.key === "ArrowLeft" ? from - 1 : from + 2);
        // Moving the button in the list takes focus from it.
        tab.focus();
    });
    return tab;
}

export function shortTabLabel(id: string, title?: string): string {
    const t = title?.trim();
    if (t) {
        return t.length > 16 ? `${t.slice(0, 15)}…` : t;
    }
    return id.slice(0, 8);
}
