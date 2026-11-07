/**
 * Side panel controls for page reading: the Send tab button and its format
 * menu, the approval prompt for an agent's read, and the short notice shown
 * after each read.
 */
import { type ApprovalChoice, type ApproveMessage, SEND_FORMATS, createApprovalQueue } from "./pageAccess";
import type { PageFormat } from "./pageContent";

export type SendTabRequest = {
    type: "agent-pane.send-tab";
    tty: string;
    format: PageFormat;
};

export type SendTabResponse = { content: string; title: string; url: string } | { error: string };

export function mountSendTab(host: HTMLElement, onSend: (format: PageFormat) => void) {
    host.className = "at-send";
    const primary = document.createElement("button");
    primary.type = "button";
    primary.className = "at-send__primary";
    primary.textContent = "Send tab";
    primary.title = "Paste this terminal's tab into it as Markdown. Does not press Enter.";
    const chevron = document.createElement("button");
    chevron.type = "button";
    chevron.className = "at-send__chevron";
    chevron.textContent = "▾";
    chevron.title = "Choose what to send";
    chevron.setAttribute("aria-haspopup", "menu");
    host.replaceChildren(primary, chevron);

    let menu: HTMLElement | null = null;
    const closeMenu = () => {
        menu?.remove();
        menu = null;
        document.removeEventListener("pointerdown", outside, true);
    };
    const outside = (e: Event) => {
        if (menu && !menu.contains(e.target as Node) && e.target !== chevron) {
            closeMenu();
        }
    };

    primary.addEventListener("click", () => onSend("markdown"));
    chevron.addEventListener("click", () => {
        if (menu) {
            closeMenu();
            return;
        }
        menu = document.createElement("div");
        menu.className = "at-launcher__menu at-send__menu";
        menu.setAttribute("role", "menu");
        for (const { format, label } of SEND_FORMATS) {
            const item = document.createElement("button");
            item.type = "button";
            item.className = "at-launcher__item";
            item.setAttribute("role", "menuitem");
            item.textContent = label;
            item.addEventListener("click", () => {
                closeMenu();
                onSend(format);
            });
            menu.appendChild(item);
        }
        document.body.appendChild(menu);
        const box = chevron.getBoundingClientRect();
        menu.style.right = `${Math.max(8, window.innerWidth - box.right)}px`;
        menu.style.bottom = `${window.innerHeight - box.top + 4}px`;
        document.addEventListener("pointerdown", outside, true);
        (menu.firstElementChild as HTMLElement | null)?.focus();
    });
}

/**
 * Shows prompts one at a time above the status bar. Each resolves with the
 * person's choice; closing the panel answers deny on the host's side. A
 * prompt the host cancels or stops waiting for is taken down.
 */
export function createApprovalPrompts(anchor: HTMLElement, terminalName: (tty: string) => string) {
    const show = () => {
        const ask = queue.current();
        if (!ask) {
            anchor.hidden = true;
            anchor.replaceChildren();
            return;
        }
        const title = document.createElement("div");
        title.className = "at-approve__title";
        title.textContent = `${ask.agent || terminalName(ask.tty)} wants to read a tab`;
        const tab = document.createElement("div");
        tab.className = "at-approve__tab";
        tab.textContent = ask.title || "(untitled)";
        const url = document.createElement("div");
        url.className = "at-approve__url";
        url.textContent = ask.url;
        const where = document.createElement("div");
        where.className = "at-approve__note";
        where.textContent = `Terminal: ${terminalName(ask.tty)}. Page text is untrusted: an agent may be told to do things by the page.`;
        const actions = document.createElement("div");
        actions.className = "at-approve__actions";
        const button = (label: string, choice: ApprovalChoice) => {
            const b = document.createElement("button");
            b.type = "button";
            b.className = `at-approve__button at-approve__button--${choice}`;
            b.textContent = label;
            b.addEventListener("click", () => queue.answer(ask.id, choice));
            return b;
        };
        actions.append(
            button("Allow this tab", "tab"),
            button("Allow all tabs for this agent", "all"),
            button("Deny", "deny"),
        );
        anchor.replaceChildren(title, tab, url, where, actions);
        anchor.hidden = false;
    };
    const queue = createApprovalQueue(show);

    return {
        ask: (ask: ApproveMessage): Promise<ApprovalChoice> => queue.ask(ask),
        cancel: (tty: string, id: string) => queue.cancel(tty, id),
    };
}

/** A short line naming the tab that was just read. */
export function createReadNotice(anchor: HTMLElement) {
    let timer: ReturnType<typeof setTimeout> | undefined;
    return (text: string, isError = false) => {
        anchor.textContent = text;
        anchor.className = "at-notice" + (isError ? " at-notice--error" : "");
        anchor.hidden = false;
        clearTimeout(timer);
        timer = setTimeout(() => {
            anchor.hidden = true;
        }, isError ? 6000 : 3500);
    };
}
