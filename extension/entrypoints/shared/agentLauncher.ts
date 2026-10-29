import { iconMarkup } from "./agentIcons";
import type { AgentInfo, RequestConfigGet, ResponseConfigGet } from "./rpc";
import type { CreateTTYOpts, TtyDimensions } from "./windowTabs";

const LAST_APP_KEY = "agent-terminal.lastApp";
const RECENT_KEY = "agent-terminal.menuRecent";
const SHELL_ID = "";

export type LauncherController = {
    setLaunchEnabled: (enabled: boolean) => void;
    refreshCatalog: () => Promise<void>;
    rememberApp: (appId: string, bump: boolean) => Promise<void>;
};

export type MenuAgent = Pick<AgentInfo, "id" | "name" | "icon" | "enabled" | "available">;

function byName(a: MenuAgent, b: MenuAgent): number {
    const name = a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    if (name !== 0) {
        return name;
    }
    return a.id.localeCompare(b.id);
}

/** A–Z, or recently opened first with the rest still A–Z. Terminal is not in this list. */
export function orderMenu(agents: MenuAgent[], recent: string[], alphabetical: boolean): MenuAgent[] {
    const visible = agents.filter((agent) => agent.enabled && agent.available);
    if (alphabetical) {
        return [...visible].sort(byName);
    }
    const left = new Map(visible.map((agent) => [agent.id, agent]));
    const bumped: MenuAgent[] = [];
    for (const id of recent) {
        const agent = left.get(id);
        if (!agent) {
            continue;
        }
        bumped.push(agent);
        left.delete(id);
    }
    return [...bumped, ...[...left.values()].sort(byName)];
}

export function bumpRecent(recent: string[], id: string): string[] {
    if (!id) {
        return recent.filter((item) => item);
    }
    return [id, ...recent.filter((item) => item && item !== id)];
}

export async function loadLastApp(): Promise<string> {
    const stored = await browser.storage.local.get<{ [LAST_APP_KEY]?: string }>(LAST_APP_KEY);
    const v = stored[LAST_APP_KEY];
    return typeof v === "string" ? v : SHELL_ID;
}

export async function saveLastApp(appId: string): Promise<void> {
    await browser.storage.local.set({ [LAST_APP_KEY]: appId });
}

async function loadRecent(): Promise<string[]> {
    const stored = await browser.storage.local.get<{ [RECENT_KEY]?: string[] }>(RECENT_KEY);
    const v = stored[RECENT_KEY];
    if (!Array.isArray(v)) {
        return [];
    }
    return v.filter((id) => typeof id === "string" && id);
}

async function saveRecent(ids: string[]): Promise<void> {
    await browser.storage.local.set({ [RECENT_KEY]: ids });
}

type Catalog = {
    agents: AgentInfo[];
    alphabetical: boolean;
};

async function fetchCatalog(): Promise<Catalog> {
    const resp = await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });
    if ("error" in resp || !Array.isArray(resp.result.agents)) {
        return { agents: [], alphabetical: false };
    }
    const agents = resp.result.agents.filter((agent) => agent && typeof agent.id === "string" && agent.id);
    const alphabetical = resp.result.config?.agentMenuAlphabetical === true;
    return { agents, alphabetical };
}

export function resolveLastApp(last: string, agents: MenuAgent[]): string {
    if (!last || last === "terminal") {
        return SHELL_ID;
    }
    const hit = agents.find((agent) => agent.id === last && agent.enabled && agent.available);
    return hit ? hit.id : SHELL_ID;
}

export function optsFromAppId(appId: string, size: TtyDimensions): CreateTTYOpts {
    if (!appId) {
        return { mode: "shell", cols: size.cols, rows: size.rows };
    }
    return { mode: "app", app: appId, cols: size.cols, rows: size.rows };
}

export function mountAgentLauncher(
    host: HTMLElement,
    handlers: {
        onLaunch: (appId: string) => void;
        canLaunch: () => boolean;
    },
): LauncherController {
    host.classList.add("at-launcher");
    host.replaceChildren();

    let agents: AgentInfo[] = [];
    let alphabetical = false;
    let recent: string[] = [];
    let lastApp = SHELL_ID;
    let menuOpen = false;
    let launchEnabled = true;
    let menuEl: HTMLElement | null = null;
    let menuItems: HTMLButtonElement[] = [];
    let activeIndex = 0;

    const primary = document.createElement("button");
    primary.type = "button";
    primary.className = "at-launcher__primary";
    primary.title = "New terminal";
    primary.setAttribute("aria-label", "New terminal");

    const chevron = document.createElement("button");
    chevron.type = "button";
    chevron.className = "at-launcher__chevron";
    chevron.title = "Choose agent";
    chevron.setAttribute("aria-label", "Choose agent");
    chevron.setAttribute("aria-haspopup", "menu");
    chevron.setAttribute("aria-expanded", "false");
    chevron.innerHTML = `<svg viewBox="0 0 12 12" width="10" height="10" aria-hidden="true"><path d="M2.5 4.5L6 8l3.5-3.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`;

    const agentById = (id: string) => agents.find((agent) => agent.id === id);

    const labelFor = (id: string) => {
        if (id === SHELL_ID) {
            return "Terminal";
        }
        return agentById(id)?.name || id;
    };

    const iconEl = (id: string): HTMLElement => {
        const wrap = document.createElement("span");
        wrap.className = "at-launcher__icon";
        const agent = id ? agentById(id) : undefined;
        wrap.innerHTML = iconMarkup(agent?.icon || "", id === SHELL_ID);
        return wrap;
    };

    const paintPrimary = () => {
        primary.replaceChildren(iconEl(lastApp));
        const plus = document.createElement("span");
        plus.className = "at-launcher__plus";
        plus.textContent = "+";
        primary.appendChild(plus);
        primary.title = `New ${labelFor(lastApp)}`;
        primary.setAttribute("aria-label", `New ${labelFor(lastApp)}`);
        primary.disabled = !launchEnabled;
    };

    const closeMenu = () => {
        if (!menuOpen) {
            return;
        }
        menuOpen = false;
        chevron.setAttribute("aria-expanded", "false");
        menuEl?.remove();
        menuEl = null;
        menuItems = [];
        document.removeEventListener("pointerdown", onDocPointer, true);
        document.removeEventListener("keydown", onMenuKey, true);
        chevron.focus({ preventScroll: true });
    };

    const onDocPointer = (e: PointerEvent) => {
        const t = e.target as Node;
        if (host.contains(t) || menuEl?.contains(t)) {
            return;
        }
        closeMenu();
    };

    const positionMenu = () => {
        if (!menuEl) {
            return;
        }
        const r = chevron.getBoundingClientRect();
        menuEl.style.width = "max-content";
        const cap = Math.min(280, window.innerWidth - 16);
        let mw = menuEl.offsetWidth;
        if (mw > cap) {
            mw = cap;
            menuEl.style.width = `${mw}px`;
        }
        let left = r.right - mw;
        if (left < 8) {
            left = 8;
        }
        const maxLeft = window.innerWidth - mw - 8;
        if (left > maxLeft) {
            left = Math.max(8, maxLeft);
        }
        menuEl.style.left = `${left}px`;
        menuEl.style.top = `${r.bottom + 4}px`;
    };

    const selectItem = async (appId: string) => {
        await remember(appId, false);
        closeMenu();
        if (handlers.canLaunch()) {
            handlers.onLaunch(appId);
        }
    };

    const onMenuKey = (e: KeyboardEvent) => {
        if (!menuOpen) {
            return;
        }
        if (e.key === "Escape") {
            e.preventDefault();
            closeMenu();
            return;
        }
        if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            if (menuItems.length === 0) {
                return;
            }
            activeIndex = e.key === "ArrowDown"
                ? (activeIndex + 1) % menuItems.length
                : (activeIndex - 1 + menuItems.length) % menuItems.length;
            menuItems[activeIndex]?.focus();
            return;
        }
        if (e.key === "Enter" || e.key === " ") {
            const el = menuItems[activeIndex];
            if (el && !el.disabled) {
                e.preventDefault();
                el.click();
            }
        }
    };

    const menuIds = () => [...orderMenu(agents, recent, alphabetical).map((agent) => agent.id), SHELL_ID];

    const openMenu = async () => {
        if (menuOpen) {
            closeMenu();
            return;
        }
        await reload();
        menuEl = document.createElement("div");
        menuEl.className = "at-launcher__menu";
        menuEl.setAttribute("role", "menu");

        const ids = menuIds();
        menuItems = [];
        ids.forEach((id, i) => {
            const item = document.createElement("button");
            item.type = "button";
            item.className = "at-launcher__item";
            item.setAttribute("role", "menuitem");
            item.appendChild(iconEl(id));
            const text = document.createElement("span");
            text.className = "at-launcher__item-label";
            text.textContent = labelFor(id);
            item.appendChild(text);
            if (id === lastApp) {
                const check = document.createElement("span");
                check.className = "at-launcher__check";
                check.textContent = "✓";
                item.appendChild(check);
            }
            item.addEventListener("click", () => void selectItem(id));
            menuEl!.appendChild(item);
            menuItems.push(item);
            if (id === lastApp) {
                activeIndex = i;
            }
        });

        document.body.appendChild(menuEl);
        positionMenu();
        menuOpen = true;
        chevron.setAttribute("aria-expanded", "true");
        document.addEventListener("pointerdown", onDocPointer, true);
        document.addEventListener("keydown", onMenuKey, true);
        window.addEventListener("resize", positionMenu, { once: true });
        menuItems[activeIndex]?.focus();
    };

    const remember = async (appId: string, bump: boolean) => {
        lastApp = resolveLastApp(appId, agents);
        await saveLastApp(lastApp);
        if (bump && lastApp) {
            recent = bumpRecent(recent, lastApp);
            await saveRecent(recent);
        }
        paintPrimary();
    };

    const reload = async () => {
        const catalog = await fetchCatalog();
        agents = catalog.agents;
        alphabetical = catalog.alphabetical;
        recent = await loadRecent();
        lastApp = resolveLastApp(await loadLastApp(), agents);
        await saveLastApp(lastApp);
        paintPrimary();
    };

    primary.addEventListener("click", () => {
        if (!launchEnabled || !handlers.canLaunch()) {
            return;
        }
        handlers.onLaunch(lastApp);
    });
    chevron.addEventListener("click", () => void openMenu());

    host.appendChild(primary);
    host.appendChild(chevron);
    paintPrimary();
    void reload();

    return {
        setLaunchEnabled(enabled: boolean) {
            launchEnabled = enabled;
            primary.disabled = !enabled;
        },
        refreshCatalog: reload,
        rememberApp: remember,
    };
}
