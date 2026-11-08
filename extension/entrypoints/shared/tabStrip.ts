import type { WindowTabState } from "./windowTabs.ts";

export type TabStripHandlers = {
    onSelect: (id: string) => void;
    onClose: (id: string) => void;
};

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
) {
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
        const tab = existing.get(id) ?? createTabButton(list.ownerDocument, id, handlers);
        existing.delete(id);

        const className = "at-tabs__tab" + (id === state.activeId ? " at-tabs__tab--active" : "");
        if (tab.className !== className) {
            tab.className = className;
        }
        const title = titles.get(id) || id;
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

function createTabButton(doc: Document, id: string, handlers: TabStripHandlers): HTMLElement {
    const tab = doc.createElement("button");
    tab.type = "button";
    tab.dataset.tabId = id;

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
    return tab;
}

export function shortTabLabel(id: string, title?: string): string {
    const t = title?.trim();
    if (t) {
        return t.length > 16 ? `${t.slice(0, 15)}…` : t;
    }
    return id.slice(0, 8);
}
