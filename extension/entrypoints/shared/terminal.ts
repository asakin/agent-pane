import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { bindKeySequences } from "./terminalKeys";
import type {
    RequestCreateTTY,
    RequestGetXtermConfig,
    RequestResizeTTY,
    ResponseCreateTTY,
    ResponseGetXtermConfig,
} from "./rpc";
import { renderSetupScreen } from "./setup";
import {
    type LauncherController,
    mountAgentLauncher,
    optsFromAppId,
} from "./agentLauncher";
import { NativeUnavailable } from "./nativeRpc";
import { type PtyAttachHandle, attachPtySocket } from "./ptyAttach";

const terminalFontFamily = "JetBrainsMono Nerd Font Mono";

async function loadTerminalFont(config: Record<string, unknown>) {
    const family = typeof config.fontFamily === "string" ? config.fontFamily : "";
    if (!family.includes(terminalFontFamily)) {
        return;
    }
    const size = typeof config.fontSize === "number" ? config.fontSize : 13;
    const face = `${size}px "${terminalFontFamily}"`;
    await Promise.all([
        document.fonts.load(face),
        document.fonts.load(`bold ${face}`),
        document.fonts.load(`italic ${face}`),
        document.fonts.load(`bold italic ${face}`),
    ]);
}
import {
    MAX_TABS,
    type WindowTabState,
    attachTTY,
    clearWindowState,
    createTTY,
    destroyTTY,
    ensureWindowAlive,
    loadWindowState,
    pruneTab,
    reclaimLegacySession,
    replaceTabId,
    resolveWindowId,
    saveWindowState,
    tabApp,
    tabCwd,
    shortTabLabel,
    type TtyDimensions,
} from "./windowTabs";

function useBlockCursor(terminal: Terminal) {
    terminal.options.cursorStyle = "block";
    // Shells ask for a thin bar. Keep the full rectangle.
    terminal.parser.registerCsiHandler({ intermediates: " ", final: "q" }, () => {
        terminal.options.cursorStyle = "block";
        return true;
    });
}

function isSidePanelPage(): boolean {
    const path = location.pathname;
    return path.endsWith("sidepanel.html") || path.includes("/sidepanel");
}

function isEphemeralLaunch(): boolean {
    return new URLSearchParams(location.search).has("app") || !isSidePanelPage();
}

async function createEphemeralTTY(size: TtyDimensions): Promise<{ id: string; url: string }> {
    const searchParams = new URLSearchParams(window.location.search);
    if (searchParams.has("app")) {
        const resp = await browser.runtime.sendMessage<RequestCreateTTY, ResponseCreateTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.create",
            params: {
                mode: "app",
                app: searchParams.get("app")!,
                args: searchParams.getAll("arg"),
                cwd: searchParams.get("cwd") || undefined,
                cols: size.cols,
                rows: size.rows,
            },
        });
        if ("error" in resp) {
            throw new Error(resp.error.message);
        }
        return resp.result;
    }
    return createTTY({ mode: "shell", cols: size.cols, rows: size.rows });
}

type TtySize = TtyDimensions & { tty: string };

function sameTtySize(a: TtySize, b: TtySize): boolean {
    return a.tty === b.tty && a.cols === b.cols && a.rows === b.rows;
}

function createResizeSender() {
    let latest: TtySize | null = null;
    let inflight = false;

    const kick = () => {
        if (inflight || !latest) {
            return;
        }
        inflight = true;
        const target = latest;
        void browser.runtime.sendMessage<RequestResizeTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.resize",
            params: { tty: target.tty, cols: target.cols, rows: target.rows },
        }).finally(() => {
            inflight = false;
            if (latest && !sameTtySize(latest, target)) {
                kick();
            }
        });
    };

    return (target: TtySize) => {
        latest = target;
        kick();
    };
}

function watchTerminalBox(
    anchor: HTMLElement,
    terminal: Terminal,
    fit: FitAddon,
    onBox: () => void,
) {
    const apply = () => {
        fit.fit();
        if (terminal.rows > 0) {
            terminal.refresh(0, terminal.rows - 1);
        }
        onBox();
    };
    apply();
    new ResizeObserver(apply).observe(anchor);
}

function showSetup(message: string) {
    document.body.className = "at-setup-host";
    document.body.innerHTML = "";
    document.body.appendChild(renderSetupScreen(message));
}

/** Rebuild tab list only — launcher host is a stable sibling. */
function renderTabList(
    list: HTMLElement,
    state: WindowTabState,
    titles: Map<string, string>,
    handlers: {
        onSelect: (id: string) => void;
        onClose: (id: string) => void;
    },
) {
    list.replaceChildren();

    for (const entry of state.tabs) {
        const id = entry.id;
        const tab = document.createElement("button");
        tab.type = "button";
        tab.className = "at-tabs__tab" + (id === state.activeId ? " at-tabs__tab--active" : "");
        tab.title = titles.get(id) || id;

        const label = document.createElement("span");
        label.className = "at-tabs__label";
        label.textContent = shortTabLabel(id, titles.get(id));
        tab.appendChild(label);

        const close = document.createElement("span");
        close.className = "at-tabs__close";
        close.textContent = "×";
        close.title = "Close terminal";
        close.addEventListener("click", (e) => {
            e.stopPropagation();
            handlers.onClose(id);
        });
        tab.appendChild(close);

        tab.addEventListener("click", () => handlers.onSelect(id));
        list.appendChild(tab);
    }
}

async function main() {
    const anchor = document.getElementById("terminal");
    if (!anchor) {
        console.error("terminal element not found");
        return;
    }

    const xtermResp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "xterm.getConfig",
        params: {
            variant: window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
        },
    });

    if ("error" in xtermResp) {
        showSetup(xtermResp.error.message);
        return;
    }

    // Ephemeral: terminal.html / popup / ?app= — no strip, no window state.
    if (isEphemeralLaunch()) {
        await loadTerminalFont(xtermResp.result);
        await runSingleTerminal(anchor, xtermResp.result);
        return;
    }

    const strip = document.getElementById("tab-strip");
    if (!strip) {
        showSetup("Tab strip missing from side panel");
        return;
    }

    let windowId: number;
    try {
        windowId = await resolveWindowId();
    } catch (err) {
        showSetup((err as Error).message);
        return;
    }

    await reclaimLegacySession(windowId);

    strip.hidden = false;
    strip.classList.add("at-tabs");
    strip.replaceChildren();

    const list = document.createElement("div");
    list.className = "at-tabs__list";
    strip.appendChild(list);

    const launcherHost = document.createElement("div");
    strip.appendChild(launcherHost);

    let launcher!: LauncherController;
    launcher = mountAgentLauncher(launcherHost, {
        onLaunch: (appId, cwd) => void newTab(appId, cwd),
        canLaunch: () => !!state && state.tabs.length < MAX_TABS,
    });

    let state = await loadWindowState(windowId);
    if (state && state.tabs.length === 0) {
        state = null;
    }

    const titles = new Map<string, string>();
    let switchGen = 0;
    let activeTtyId = "";
    const scheduleResize = createResizeSender();
    let ws: WebSocket | null = null;
    let ptyAttach: PtyAttachHandle | null = null;

    await loadTerminalFont(xtermResp.result);
    const terminal = new Terminal(xtermResp.result);
    useBlockCursor(terminal);
    bindKeySequences(terminal);
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(anchor);

    terminal.onResize((size) => {
        if (!activeTtyId) {
            return;
        }
        scheduleResize({ tty: activeTtyId, cols: size.cols, rows: size.rows });
    });

    try {
        const webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
            webglAddon.dispose();
        });
        terminal.loadAddon(webglAddon);
    } catch {
        // Canvas renderer remains the default.
    }

    const refreshStrip = () => {
        renderTabList(list, state!, titles, {
            onSelect: (id) => void switchTo(id),
            onClose: (id) => void closeTab(id),
        });
        launcher.setLaunchEnabled(state!.tabs.length < MAX_TABS);
    };

    function hasTab(id: string): boolean {
        return !!state && state.tabs.some((tab) => tab.id === id);
    }

    async function openKind(app: string, size: TtyDimensions, remember: boolean, cwd?: string): Promise<{ id: string; url: string; app: string; cwd?: string }> {
        try {
            const created = await createTTY(optsFromAppId(app, size, cwd));
            if (remember) {
                await launcher.rememberApp(app, app !== "");
            }
            return { ...created, app, cwd };
        } catch (err) {
            if (!remember) {
                throw err;
            }
            await launcher.rememberApp("", false);
            const created = await createTTY({ mode: "shell", cwd, cols: size.cols, rows: size.rows });
            return { ...created, app: "", cwd };
        }
    }

    const detachSocket = () => {
        if (ptyAttach) {
            try {
                ptyAttach.dispose();
            } catch {
                // already disposed
            }
            ptyAttach = null;
        }
        if (ws) {
            ws.onclose = null;
            try {
                ws.close();
            } catch {
                // ignore
            }
            ws = null;
        }
    };

    const clearTerminalBuffer = () => {
        terminal.reset();
    };

    async function attachActive(gen: number): Promise<boolean> {
        if (gen !== switchGen || !state) {
            return false;
        }

        let endpoint = await attachTTY(state.activeId);
        if (!endpoint) {
            const deadId = state.activeId;
            const kind = tabApp(state, deadId);
            const size = fitAddon.proposeDimensions();
            if (!size) {
                throw new Error("Terminal has no size yet");
            }
            const created = await createTTY(optsFromAppId(kind, size, tabCwd(state, deadId)));
            if (gen !== switchGen) {
                await destroyTTY(created.id);
                return false;
            }
            await ensureWindowAlive(windowId, created.id);
            state = replaceTabId(state, deadId, created.id);
            await saveWindowState(windowId, state);
            refreshStrip();
            endpoint = await attachTTY(state.activeId);
            if (!endpoint) {
                throw new Error("Could not attach to terminal session");
            }
        }

        if (gen !== switchGen) {
            return false;
        }

        detachSocket();
        clearTerminalBuffer();

        activeTtyId = endpoint.id;
        ws = new WebSocket(endpoint.url);
        const attachedId = endpoint.id;
        const attachGen = gen;
        ptyAttach = attachPtySocket(terminal, ws, {
            isCurrent: () => attachGen === switchGen && ws?.readyState === WebSocket.OPEN,
            onClose: () => {
                if (gen !== switchGen || !state) {
                    return;
                }
                if (!hasTab(attachedId) || state.activeId !== attachedId) {
                    return;
                }
                void (async () => {
                    if (gen !== switchGen) {
                        return;
                    }
                    try {
                        await attachActive(gen);
                    } catch (err) {
                        if (gen === switchGen) {
                            showSetup((err as Error).message);
                        }
                    }
                })();
            },
        });

        const size = fitAddon.proposeDimensions();
        if (size) {
            scheduleResize({ tty: activeTtyId, cols: size.cols, rows: size.rows });
        }

        if (gen !== switchGen) {
            return false;
        }
        terminal.focus();
        return true;
    }

    async function switchTo(id: string) {
        if (!state || !hasTab(id)) {
            return;
        }
        if (id === state.activeId && activeTtyId === id && ws?.readyState === WebSocket.OPEN) {
            terminal.focus();
            return;
        }
        const gen = ++switchGen;
        state = { ...state, activeId: id };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            await attachActive(gen);
        } catch (err) {
            if (gen !== switchGen) {
                return;
            }
            if (err instanceof NativeUnavailable) {
                return;
            }
            console.error("switch failed:", err);
            showSetup((err as Error).message);
        }
    }

    async function newTab(appId?: string, cwd?: string) {
        if (!state || state.tabs.length >= MAX_TABS) {
            return;
        }
        const size = fitAddon.proposeDimensions();
        if (!size) {
            return;
        }
        const gen = ++switchGen;
        let created: { id: string; url: string; app: string; cwd?: string };
        try {
            created = await openKind(appId ?? "", size, true, cwd);
            await ensureWindowAlive(windowId, created.id);
        } catch (err) {
            if (!(err instanceof NativeUnavailable)) {
                showSetup((err as Error).message);
            }
            return;
        }
        if (gen !== switchGen) {
            await destroyTTY(created.id);
            return;
        }
        state = {
            tabs: [...state.tabs, { id: created.id, app: created.app, cwd: created.cwd }],
            activeId: created.id,
        };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            await attachActive(gen);
        } catch (err) {
            if (gen === switchGen && !(err instanceof NativeUnavailable)) {
                showSetup((err as Error).message);
            }
        }
    }

    async function closeTab(id: string) {
        if (!state) {
            return;
        }

        if (id !== state.activeId) {
            await destroyTTY(id);
            titles.delete(id);
            const next = pruneTab(state, id);
            if (!next) {
                return;
            }
            state = next;
            await saveWindowState(windowId, state);
            refreshStrip();
            return;
        }

        const idx = state.tabs.findIndex((tab) => tab.id === id);
        const neighbor = state.tabs[idx + 1] ?? state.tabs[idx - 1];

        if (!neighbor) {
            const size = fitAddon.proposeDimensions();
            if (!size) {
                await clearWindowState(windowId);
                showSetup("Terminal has no size yet");
                return;
            }
            const gen = ++switchGen;
            await destroyTTY(id);
            titles.delete(id);
            let created: { id: string; url: string };
            try {
                created = await createTTY({ mode: "shell", cols: size.cols, rows: size.rows });
                await ensureWindowAlive(windowId, created.id);
            } catch (err) {
                await clearWindowState(windowId);
                showSetup((err as Error).message);
                return;
            }
            if (gen !== switchGen) {
                await destroyTTY(created.id);
                return;
            }
            state = { tabs: [{ id: created.id, app: "" }], activeId: created.id };
            await saveWindowState(windowId, state);
            refreshStrip();
            try {
                await attachActive(gen);
            } catch (err) {
                if (gen === switchGen) {
                    showSetup((err as Error).message);
                }
            }
            return;
        }

        const gen = ++switchGen;
        state = { tabs: state.tabs.filter((tab) => tab.id !== id), activeId: neighbor.id };
        await saveWindowState(windowId, state);
        refreshStrip();
        try {
            const ok = await attachActive(gen);
            if (ok && gen === switchGen) {
                await destroyTTY(id);
                titles.delete(id);
            }
        } catch (err) {
            if (gen === switchGen && !(err instanceof NativeUnavailable)) {
                showSetup((err as Error).message);
            }
        }
    }

    terminal.onTitleChange((title) => {
        if (activeTtyId) {
            titles.set(activeTtyId, title);
            document.title = `${title}  |  Agent Terminal`;
            refreshStrip();
        }
    });

    globalThis.onbeforeunload = () => {
        detachSocket();
    };

    globalThis.onfocus = () => {
        // Don't steal focus from the agent menu.
        if (document.querySelector(".at-launcher__menu")) {
            return;
        }
        terminal.focus();
    };

    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", async (event) => {
        const variant = event.matches ? "dark" : "light";
        const resp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "xterm.getConfig",
            params: { variant },
        });
        if ("error" in resp) {
            return;
        }
        terminal.options.theme = resp.result.theme;
    });

    let createStarted = false;
    async function createInitial(size: TtyDimensions) {
        if (createStarted || state) {
            return;
        }
        createStarted = true;
        const gen = ++switchGen;
        try {
            const created = await createTTY({ mode: "shell", cols: size.cols, rows: size.rows });
            await ensureWindowAlive(windowId, created.id);
            if (gen !== switchGen) {
                await destroyTTY(created.id);
                return;
            }
            state = { tabs: [{ id: created.id, app: "" }], activeId: created.id };
            await saveWindowState(windowId, state);
            refreshStrip();
            await attachActive(gen);
        } catch (err) {
            if (gen === switchGen && !(err instanceof NativeUnavailable)) {
                showSetup((err as Error).message);
            }
        }
    }

    watchTerminalBox(anchor, terminal, fitAddon, () => {
        if (state) {
            return;
        }
        const size = fitAddon.proposeDimensions();
        if (!size) {
            return;
        }
        void createInitial(size);
    });

    if (state) {
        refreshStrip();
        const gen = ++switchGen;
        try {
            await attachActive(gen);
        } catch (err) {
            showSetup((err as Error).message);
        }
    }
}

async function runSingleTerminal(
    anchor: HTMLElement,
    xtermConfig: Record<string, unknown>,
) {
    let activeTtyId = "";
    const scheduleResize = createResizeSender();
    const terminal = new Terminal(xtermConfig);
    useBlockCursor(terminal);
    bindKeySequences(terminal);
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(anchor);

    terminal.onResize((size) => {
        if (!activeTtyId) {
            return;
        }
        scheduleResize({ tty: activeTtyId, cols: size.cols, rows: size.rows });
    });

    try {
        const webglAddon = new WebglAddon();
        webglAddon.onContextLoss(() => {
            webglAddon.dispose();
        });
        terminal.loadAddon(webglAddon);
    } catch {
        // Canvas fallback.
    }

    let ws: WebSocket | null = null;
    let ptyAttach: PtyAttachHandle | null = null;
    let ephemeralUnloading = false;
    let booting = false;

    const bindSocket = (socket: WebSocket, id: string) => {
        ptyAttach?.dispose();
        ws = socket;
        activeTtyId = id;
        ptyAttach = attachPtySocket(terminal, socket, {
            isCurrent: () => !ephemeralUnloading && ws === socket,
            onClose: () => {
                if (ephemeralUnloading) {
                    return;
                }
                void (async () => {
                    try {
                        const again = await attachTTY(id);
                        if (again && !ephemeralUnloading) {
                            bindSocket(new WebSocket(again.url), again.id);
                            fitAddon.fit();
                            terminal.focus();
                            return;
                        }
                    } catch {
                        // fall through
                    }
                    if (ephemeralUnloading) {
                        return;
                    }
                    try {
                        const size = fitAddon.proposeDimensions();
                        if (!size) {
                            return;
                        }
                        const app = new URLSearchParams(location.search).get("app") || "";
                        const created = await createTTY(optsFromAppId(app, size));
                        bindSocket(new WebSocket(created.url), created.id);
                        scheduleResize({ tty: created.id, cols: size.cols, rows: size.rows });
                        terminal.focus();
                    } catch (err) {
                        console.error("shell recover failed:", err);
                    }
                })();
            },
        });
    };

    watchTerminalBox(anchor, terminal, fitAddon, () => {
        if (activeTtyId || booting) {
            return;
        }
        const size = fitAddon.proposeDimensions();
        if (!size) {
            return;
        }
        booting = true;
        void (async () => {
            try {
                const tty = await createEphemeralTTY(size);
                if (ephemeralUnloading) {
                    return;
                }
                bindSocket(new WebSocket(tty.url), tty.id);
                terminal.focus();
            } catch (err) {
                showSetup((err as Error).message);
            }
        })();
    });

    globalThis.onbeforeunload = () => {
        ephemeralUnloading = true;
        ptyAttach?.dispose();
        ptyAttach = null;
        if (ws) {
            try {
                ws.close();
            } catch {
                // ignore
            }
        }
    };
    terminal.onTitleChange((title) => {
        document.title = `${title}  |  Agent Terminal`;
    });
    globalThis.onfocus = () => {
        terminal.focus();
    };
    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", async (event) => {
        const variant = event.matches ? "dark" : "light";
        const resp = await browser.runtime.sendMessage<RequestGetXtermConfig, ResponseGetXtermConfig>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "xterm.getConfig",
            params: { variant },
        });
        if ("error" in resp) {
            return;
        }
        terminal.options.theme = resp.result.theme;
    });
    terminal.focus();
}

void main();
