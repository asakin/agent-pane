import { errorFromResponse, NativeUnavailable } from "./nativeRpc";
import type {
    RequestAttachTTY,
    RequestCreateTTY,
    RequestDestroyTTY,
    ResponseAttachTTY,
    ResponseCreateTTY,
} from "./rpc";

export const MAX_TABS = 8;

export type WindowTab = {
    id: string;
    app: string;
};

export type WindowTabState = {
    tabs: WindowTab[];
    activeId: string;
};

function isWindowTab(value: unknown): value is WindowTab {
    if (!value || typeof value !== "object") {
        return false;
    }
    const tab = value as WindowTab;
    return typeof tab.id === "string" && tab.id !== "" && typeof tab.app === "string";
}

export function windowStateKey(windowId: number): string {
    return `agent-terminal.window.${windowId}`;
}

export function legacySessionKey(windowId: number): string {
    return `agent-terminal.ttySession.${windowId}`;
}

export async function resolveWindowId(): Promise<number> {
    const win = await browser.windows.getCurrent();
    if (win.id == null) {
        throw new Error("Could not resolve Chrome window id");
    }
    return win.id;
}

export async function loadWindowState(windowId: number): Promise<WindowTabState | null> {
    const key = windowStateKey(windowId);
    const stored = await browser.storage.session.get<{ [key: string]: WindowTabState }>(key);
    const state = stored[key];
    if (!state || !Array.isArray(state.tabs) || typeof state.activeId !== "string") {
        return null;
    }
    if (!state.tabs.every(isWindowTab)) {
        return null;
    }
    if (!state.tabs.some((tab) => tab.id === state.activeId)) {
        return null;
    }
    return state;
}

export async function saveWindowState(windowId: number, state: WindowTabState): Promise<void> {
    await browser.storage.session.set({ [windowStateKey(windowId)]: state });
}

export async function clearWindowState(windowId: number): Promise<void> {
    await browser.storage.session.remove(windowStateKey(windowId));
}

export async function destroyTTY(id: string): Promise<void> {
    try {
        await browser.runtime.sendMessage<RequestDestroyTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.destroy",
            params: { id },
        });
    } catch {
        // Host may already be gone.
    }
}

export type TtyDimensions = {
    cols: number;
    rows: number;
};

export type CreateTTYOpts =
    | ({ mode: "shell" } & TtyDimensions)
    | ({ mode: "app"; app: string } & TtyDimensions);

export async function createTTY(opts: CreateTTYOpts): Promise<{ id: string; url: string }> {
    const params = opts.mode === "app"
        ? { mode: "app" as const, app: opts.app, cols: opts.cols, rows: opts.rows }
        : { mode: "shell" as const, cols: opts.cols, rows: opts.rows };
    const resp = await browser.runtime.sendMessage<RequestCreateTTY, ResponseCreateTTY>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "tty.create",
        params,
    });
    if ("error" in resp) {
        throw errorFromResponse(resp);
    }
    return resp.result;
}

export async function attachTTY(id: string): Promise<{ id: string; url: string } | null> {
    const resp = await browser.runtime.sendMessage<RequestAttachTTY, ResponseAttachTTY>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "tty.attach",
        params: { id },
    });
    if ("error" in resp) {
        const failure = errorFromResponse(resp);
        if (failure instanceof NativeUnavailable) {
            throw failure;
        }
        return null;
    }
    return resp.result;
}

/** Destroy orphan from pre-tabs single-session key, if any. */
export async function reclaimLegacySession(windowId: number): Promise<void> {
    const key = legacySessionKey(windowId);
    const stored = await browser.storage.session.get<{ [key: string]: string }>(key);
    const id = stored[key];
    if (!id) {
        return;
    }
    await browser.storage.session.remove(key);
    await destroyTTY(id);
}

export async function ensureWindowAlive(windowId: number, ttyId: string): Promise<void> {
    try {
        await browser.windows.get(windowId);
    } catch {
        await destroyTTY(ttyId);
        throw new Error("Chrome window closed before the terminal could attach");
    }
}

export function shortTabLabel(id: string, title?: string): string {
    const t = title?.trim();
    if (t) {
        return t.length > 16 ? `${t.slice(0, 15)}…` : t;
    }
    return id.slice(0, 8);
}

/** Drop a dead id; pick a new activeId. Returns null if tabs empty. */
export function pruneTab(state: WindowTabState, deadId: string): WindowTabState | null {
    const deadIndex = state.tabs.findIndex((tab) => tab.id === deadId);
    const tabs = state.tabs.filter((tab) => tab.id !== deadId);
    const fallback = tabs[0];
    if (!fallback) {
        return null;
    }
    let activeId = state.activeId;
    if (activeId === deadId) {
        const neighbor = state.tabs[deadIndex + 1] ?? state.tabs[deadIndex - 1];
        activeId = neighbor && neighbor.id !== deadId ? neighbor.id : fallback.id;
    }
    if (!tabs.some((tab) => tab.id === activeId)) {
        activeId = fallback.id;
    }
    return { tabs, activeId };
}

export function replaceTabId(state: WindowTabState, oldId: string, nextId: string): WindowTabState {
    return {
        tabs: state.tabs.map((tab) => tab.id === oldId ? { ...tab, id: nextId } : tab),
        activeId: state.activeId === oldId ? nextId : state.activeId,
    };
}

export function tabApp(state: WindowTabState, id: string): string {
    return state.tabs.find((tab) => tab.id === id)?.app ?? "";
}
