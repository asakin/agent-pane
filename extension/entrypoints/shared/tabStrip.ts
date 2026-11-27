import type { WindowTabState } from "./windowTabs.ts";
import { type PaneStatus, statusMark } from "./paneStatus.ts";

export type TabStripHandlers = {
    onSelect: (id: string) => void;
    onClose: (id: string) => void;
    /** Moves the pane tab to slot, a place between the tabs as they are now (0 is first, tabs.length last). */
    onMove?: (id: string, slot: number) => void;
    /** A person renamed the pane tab; "" returns it to its terminal's title. */
    onRename?: (id: string, name: string) => void;
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
        // A name the person set wins over the terminal's title.
        const shown = entry.name || titles.get(id);
        const named = shown || id;
        const title = mark ? `${named}: ${mark.text}` : named;
        if (tab.title !== title) {
            tab.title = title;
        }
        const label = tab.querySelector(".at-tabs__label");
        const text = shortTabLabel(id, shown);
        if (label && label.textContent !== text) {
            label.textContent = text;
        }
        tab.dataset.name = entry.name ?? "";

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

    // Double-click or F2 edits the name in place.
    const rename = () => {
        if (!listHandlers.get(list)?.onRename || tab.ownerDocument.querySelector(".at-tabs__rename")) {
            return;
        }
        const current = tab.dataset.name || tab.querySelector(".at-tabs__label")?.textContent || "";
        editTabName(tab, current, (name) => listHandlers.get(list)?.onRename?.(id, name));
    };
    tab.addEventListener("dblclick", (e) => {
        if (close.contains(e.target as Node)) {
            return;
        }
        e.preventDefault();
        rename();
    });
    tab.addEventListener("keydown", (e) => {
        if ((e as KeyboardEvent).key === "F2") {
            e.preventDefault();
            rename();
        }
    });
    return tab;
}

/** The longest pane tab name a person can set, in characters. */
export const MAX_TAB_NAME = 100;

/**
 * A pane tab name as a person typed it, on one line and at most
 * MAX_TAB_NAME characters. "" means the pane tab follows its terminal's title.
 */
export function cleanTabName(name: string): string {
    const line = name.replace(/[\u0000-\u001f\u007f-\u009f]/g, " ").trim();
    return Array.from(line).slice(0, MAX_TAB_NAME).join("").trim();
}

/**
 * Gives a pane tab the name a person typed. An empty name returns it to its
 * terminal's title.
 */
export function setTabName<T extends { id: string; name?: string }>(
    state: { tabs: T[]; activeId: string },
    id: string,
    name: string,
): { tabs: T[]; activeId: string } {
    const clean = cleanTabName(name);
    return {
        tabs: state.tabs.map((tab) => {
            if (tab.id !== id) {
                return tab;
            }
            const { name: _old, ...rest } = tab;
            return (clean ? { ...rest, name: clean } : rest) as T;
        }),
        activeId: state.activeId,
    };
}

/**
 * Edits a pane tab's name in a field over its button, which is left alone so
 * the strip can keep updating it. Enter, or leaving the field, saves; Escape
 * cancels. save is called only when the text changed.
 */
export function editTabName(tab: HTMLElement, current: string, save: (name: string) => void): HTMLInputElement {
    const doc = tab.ownerDocument;
    const input = doc.createElement("input");
    input.type = "text";
    input.className = "at-tabs__rename";
    input.value = current;
    input.maxLength = MAX_TAB_NAME;
    input.spellcheck = false;
    input.setAttribute("aria-label", "Pane tab name (empty for the terminal's title)");

    let open = true;
    const finish = (keep: boolean, refocus: boolean) => {
        if (!open) {
            return;
        }
        open = false;
        input.remove();
        if (keep && input.value !== current) {
            save(input.value);
        }
        if (refocus) {
            tab.focus?.();
        }
    };
    input.addEventListener("keydown", (e) => {
        e.stopPropagation();
        // Enter and Escape during IME composition belong to the composition.
        if (e.isComposing || e.keyCode === 229) {
            return;
        }
        if (e.key === "Enter") {
            e.preventDefault();
            finish(true, true);
        } else if (e.key === "Escape") {
            e.preventDefault();
            finish(false, true);
        }
    });
    input.addEventListener("blur", () => finish(true, false));

    const r = tab.getBoundingClientRect?.();
    if (r) {
        input.style.top = `${r.top}px`;
        input.style.left = `${r.left}px`;
        input.style.width = `${Math.max(r.width, 120)}px`;
        input.style.height = `${r.height}px`;
    }
    doc.body.appendChild(input);
    input.focus?.();
    input.select?.();
    return input;
}

export function shortTabLabel(id: string, title?: string): string {
    const t = title?.trim();
    if (t) {
        return t.length > 16 ? `${t.slice(0, 15)}…` : t;
    }
    return id.slice(0, 8);
}
