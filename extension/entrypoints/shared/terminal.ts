import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebglAddon } from "@xterm/addon-webgl";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { bindKeySequences } from "./terminalKeys";
import type {
    RequestConfigGet,
    RequestConfigSet,
    RequestCreateTTY,
    RequestGetXtermConfig,
    RequestInitialize,
    RequestListTTY,
    RequestResizeTTY,
    ResponseConfigGet,
    ResponseConfigSet,
    ResponseCreateTTY,
    ResponseGetXtermConfig,
    ResponseInitialize,
    ResponseListTTY,
} from "./rpc";
import { renderSetupScreen, renderUpdateScreen } from "./setup";
import { checkHost } from "./hostVersion";
import {
    type LauncherController,
    mountAgentLauncher,
    optsFromAppId,
} from "./agentLauncher";
import { NativeUnavailable } from "./nativeRpc";
import { renderTabList } from "./tabStrip";
import { type PtyAttachHandle, attachPtySocket } from "./ptyAttach";
import { RECONNECT_GAVE_UP, createReconnectBackoff } from "./reconnect";
import { canStepFontSize, clampFontSize, configWithFontSize, stepFontSize } from "./fontSize";
import {
    type ChromeTabLike,
    concernsAgentTab,
    type TabLifecycleEvent,
    applyTabLifecycle,
    statusView,
    toggleLock,
    trackedTabId,
} from "./tabLock";
import type { PageFormat } from "./pageContent";
import { assertTabNotBlocked, isApproveMessage, isCancelMessage, isReadNotice, pasteBytes, readTabDocument, sendTabContent } from "./pageAccess";
import { blockedDomainsFromConfig } from "./blockedDomains";
import { createApprovalPrompts, createReadNotice, mountSendTab } from "./pagePanel";
import { isOwnExtensionPage } from "./sender";
import { describeUrl } from "./tabLock";
import { type PaneStatus, isStatusMessage, parseStatus } from "./paneStatus";

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
    type WindowTab,
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
    tabPane,
    resolveWindowId,
    saveWindowState,
    tabApp,
    tabCwd,
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

/** The Blocked domains list as config.json has it now. */
async function currentBlockedDomains(): Promise<string[]> {
    return blockedDomainsFromConfig(await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    }));
}

async function saveFontSize(size: number) {
    const current = await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });
    if ("error" in current) {
        throw new Error(current.error.message);
    }
    const resp = await browser.runtime.sendMessage<RequestConfigSet, ResponseConfigSet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.set",
        params: { config: configWithFontSize(current.result.config, size) },
    });
    if ("error" in resp) {
        throw new Error(resp.error.message);
    }
}

/** Top-right −/+ buttons that change the terminal font size live. */
function mountFontSizeControls(
    host: HTMLElement,
    terminal: Terminal,
    fit: FitAddon,
    xtermConfig: Record<string, unknown>,
) {
    host.className = "at-fontsize";

    const makeButton = (label: string, title: string, delta: number) => {
        const button = document.createElement("button");
        button.type = "button";
        button.className = "at-fontsize__button";
        button.textContent = label;
        button.title = title;
        button.setAttribute("aria-label", title);
        // Keep focus in the terminal instead of the button.
        button.addEventListener("mousedown", (e) => e.preventDefault());
        button.addEventListener("click", () => void change(delta));
        host.appendChild(button);
        return button;
    };

    const smaller = makeButton("−", "Decrease font size", -1);
    const larger = makeButton("+", "Increase font size", 1);

    let size = clampFontSize(terminal.options.fontSize);
    let saveChain: Promise<void> = Promise.resolve();

    const sync = () => {
        smaller.disabled = !canStepFontSize(size, -1);
        larger.disabled = !canStepFontSize(size, 1);
    };

    async function change(delta: number) {
        const next = stepFontSize(size, delta);
        if (next === size) {
            terminal.focus();
            return;
        }
        size = next;
        sync();
        await loadTerminalFont({ ...xtermConfig, fontSize: next });
        if (size !== next) {
            return;
        }
        terminal.options.fontSize = next;
        // fit() resizes cols/rows; onResize then sends tty.resize.
        fit.fit();
        if (terminal.rows > 0) {
            terminal.refresh(0, terminal.rows - 1);
        }
        terminal.focus();
        saveChain = saveChain
            .then(() => saveFontSize(next))
            .catch((err) => console.error("font size save failed:", err));
    }

    sync();
}

function showSetup(message: string) {
    document.body.className = "at-setup-host";
    document.body.innerHTML = "";
    document.body.appendChild(renderSetupScreen(message));
}

function showUpdate(found: string, needed: string) {
    document.body.className = "at-setup-host";
    document.body.innerHTML = "";
    document.body.appendChild(renderUpdateScreen(found, needed, async () => {
        await initializeHost(true);
    }));
}

function initializeHost(reconnect = false) {
    return browser.runtime.sendMessage<RequestInitialize, ResponseInitialize>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "initialize",
        params: { reconnect },
    });
}

function renderStatusBar(bar: HTMLElement, tab: WindowTab | undefined, chromeTab: ChromeTabLike | undefined) {
    const view = statusView(tab, chromeTab);
    bar.className = `at-status at-status--${view.mode}`;
    bar.title = view.tooltip;
    const icon = document.createElement("span");
    icon.className = "at-status__icon";
    icon.textContent = view.mode === "following" ? "\u{1F513}" : view.mode === "locked" ? "\u{1F512}" : "\u26A0";
    const text = document.createElement("span");
    text.className = "at-status__text";
    text.textContent = view.text;
    bar.replaceChildren(icon, text);
    bar.setAttribute("aria-label", view.tooltip);
}

async function main() {
    const anchor = document.getElementById("terminal");
    if (!anchor) {
        console.error("terminal element not found");
        return;
    }

    // No terminal is created or attached until the host is new enough.
    const hostResp = await initializeHost();
    if ("error" in hostResp) {
        showSetup(hostResp.error.message);
        return;
    }
    const host = checkHost(hostResp.result);
    if (!host.ok) {
        showUpdate(host.version, host.neededVersion);
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
    const statusBar = document.getElementById("status-bar");
    const sendTabHost = document.getElementById("send-tab");
    const approveHost = document.getElementById("page-approve");
    const noticeHost = document.getElementById("page-notice");
    if (!strip || !statusBar || !sendTabHost || !approveHost || !noticeHost) {
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
        canLaunch: () => !!state,
    });

    const fontSizeHost = document.createElement("div");
    strip.appendChild(fontSizeHost);

    // Listen before reading saved locks, so a locked Chrome tab that closes or
    // is replaced while the panel starts up is not missed. Events queue until
    // the status bar below takes over.
    let onTabLifecycle: ((event: TabLifecycleEvent) => void) | undefined;
    const earlyTabEvents: TabLifecycleEvent[] = [];
    const tabLifecycle = (event: TabLifecycleEvent) => {
        if (onTabLifecycle) {
            onTabLifecycle(event);
        } else {
            earlyTabEvents.push(event);
        }
    };
    browser.tabs.onRemoved.addListener((tabId) => tabLifecycle({ removed: tabId }));
    browser.tabs.onReplaced.addListener((addedTabId, removedTabId) => tabLifecycle({ removed: removedTabId, added: addedTabId }));

    let state = await loadWindowState(windowId);
    if (state && state.tabs.length === 0) {
        state = null;
    }

    const titles = new Map<string, string>();
    // Pane tabs whose title in titles came from the host, not the terminal.
    const hostTitled = new Set<string>();
    // What each pane tab's program reports it is doing (Durable only).
    const statuses = new Map<string, PaneStatus>();
    // When each pane tab last had a live tty.event, counted by liveSeq, so a
    // tty.list answer does not replace facts newer than it.
    let liveSeq = 0;
    const liveAt = new Map<string, number>();
    let switchGen = 0;
    let activeTtyId = "";
    const scheduleResize = createResizeSender();
    let ws: WebSocket | null = null;
    let ptyAttach: PtyAttachHandle | null = null;
    const reconnect = createReconnectBackoff();

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

    mountFontSizeControls(fontSizeHost, terminal, fitAddon, xtermResp.result);

    // Chrome tab tracking for the bottom status bar.
    let activeChromeTabId: number | undefined;
    let shownChromeTabId: number | undefined;
    // Agent tab the bar currently shows. Lags state.activeId while a switch
    // or lookup is in flight.
    let shownAgentId: string | undefined;
    let statusGen = 0;

    const activeAgentTab = (): WindowTab | undefined =>
        state?.tabs.find((tab) => tab.id === state!.activeId);

    async function refreshStatus(known?: ChromeTabLike) {
        const gen = ++statusGen;
        const agentTab = activeAgentTab();
        const id = trackedTabId(agentTab, activeChromeTabId);
        let chromeTab: ChromeTabLike | undefined;
        if (id !== undefined) {
            if (known?.id === id) {
                chromeTab = known;
            } else {
                try {
                    chromeTab = await browser.tabs.get(id);
                } catch {
                    chromeTab = undefined;
                }
            }
        }
        if (gen !== statusGen) {
            return;
        }
        shownChromeTabId = chromeTab?.id;
        shownAgentId = agentTab?.id;
        renderStatusBar(statusBar!, agentTab, chromeTab);
    }

    async function updateLocks(next: WindowTabState) {
        if (next === state) {
            return;
        }
        state = next;
        await saveWindowState(windowId, state);
    }

    statusBar.hidden = false;
    statusBar.addEventListener("click", () => {
        if (!state) {
            return;
        }
        // The bar still shows another agent tab: acting on it would apply that
        // tab's lock to this one. Let the refresh catch up instead.
        if (shownAgentId !== state.activeId) {
            void refreshStatus();
            return;
        }
        void updateLocks(toggleLock(state, state.activeId, shownChromeTabId)).then(() => refreshStatus());
    });

    browser.tabs.onActivated.addListener((info) => {
        if (info.windowId !== windowId) {
            return;
        }
        activeChromeTabId = info.tabId;
        void refreshStatus();
    });

    browser.tabs.onUpdated.addListener((_tabId, _change, chromeTab) => {
        if (concernsAgentTab(activeAgentTab(), windowId, activeChromeTabId, chromeTab)) {
            void refreshStatus(chromeTab);
        }
    });

    onTabLifecycle = (event) => {
        if (activeChromeTabId === event.removed) {
            activeChromeTabId = event.added;
        }
        if (state) {
            void updateLocks(applyTabLifecycle(state, [event]));
        }
        void refreshStatus();
    };
    if (state && earlyTabEvents.length > 0) {
        void updateLocks(applyTabLifecycle(state, earlyTabEvents.splice(0)));
    }

    // Page reading. Send tab is the person's own action, so it needs no
    // prompt; reads an agent asks for are approved here, per terminal.
    const notice = createReadNotice(noticeHost);
    const terminalName = (tty: string) => titles.get(tty) || tabApp(state ?? { tabs: [], activeId: "" }, tty) || "Terminal";
    const prompts = createApprovalPrompts(approveHost, terminalName);

    async function sendTab(format: PageFormat) {
        const agentTab = activeAgentTab();
        if (!state || !agentTab || shownAgentId !== agentTab.id) {
            return;
        }
        const tabId = trackedTabId(agentTab, activeChromeTabId);
        if (tabId === undefined) {
            notice("No tab to send: the locked tab was closed", true);
            return;
        }
        const tty = agentTab.id;
        const gen = switchGen;
        // The same checks as an agent's read, with no approval to bypass.
        const pageApis = { tabs: browser.tabs, scripting: browser.scripting, blockedDomains: currentBlockedDomains };
        try {
            const sent = await sendTabContent({
                read: () => readTabDocument(pageApis, tabId, format, false),
                describe: async () => {
                    const tab = await browser.tabs.get(tabId).catch(() => undefined);
                    return tab?.title || describeUrl(tab?.url ?? "");
                },
                checkBlocked: (sourceUrl) => assertTabNotBlocked(pageApis, tabId, sourceUrl),
                // Still the pane tab it was clicked for and attached; the
                // attachment itself says whether it is taking input yet.
                isDestination: () => !!state && state.activeId === tty && activeTtyId === tty
                    && gen === switchGen && ws?.readyState === WebSocket.OPEN && !!ptyAttach,
                deliver: (content) => {
                    const bytes = pasteBytes(content, terminal.modes.bracketedPasteMode);
                    if (!ptyAttach?.sendInput(bytes)) {
                        return false;
                    }
                    terminal.scrollToBottom();
                    return true;
                },
            });
            if (sent === null) {
                notice("Not sent: the pane tab changed or was still reconnecting while the Chrome tab was read", true);
                return;
            }
            terminal.focus();
            notice(`Sent ${sent}`);
        } catch (err) {
            notice((err as Error).message, true);
        }
    }

    mountSendTab(sendTabHost, (format) => void sendTab(format));
    sendTabHost.hidden = false;

    browser.runtime.onMessage.addListener((msg, sender, sendResponse) => {
        if (!isOwnExtensionPage(sender, browser.runtime.id, browser.runtime.getURL("/"))) {
            return false;
        }
        // Only the panel holding the terminal answers for it.
        if (isApproveMessage(msg) && hasTab(msg.tty)) {
            void prompts.ask(msg).then((choice) => sendResponse({ choice }));
            return true;
        }
        if (isCancelMessage(msg) && hasTab(msg.tty)) {
            prompts.cancel(msg.tty, msg.id);
            return false;
        }
        if (isReadNotice(msg) && hasTab(msg.tty)) {
            notice(`${terminalName(msg.tty)} read ${msg.title || describeUrl(msg.url)}`);
        }
        if (isStatusMessage(msg) && hasTab(msg.event.tty)) {
            liveAt.set(msg.event.tty, ++liveSeq);
            applyFacts(msg.event.tty, msg.event.status, msg.event.title);
        }
        return false;
    });

    // Records what a pane tab's program reports. A title the host knows
    // stands in until the attached terminal sets one itself; an empty one
    // clears a title the host gave.
    function applyFacts(tty: string, status: PaneStatus | null, title?: string) {
        if (status) {
            statuses.set(tty, status);
        } else {
            statuses.delete(tty);
        }
        if (title !== undefined && tty !== activeTtyId) {
            if (title) {
                titles.set(tty, title);
                hostTitled.add(tty);
            } else if (hostTitled.delete(tty)) {
                titles.delete(tty);
            }
        }
        if (state) {
            refreshStrip();
        }
    }

    // What pane tabs reported before this panel opened, or, given one id,
    // before that new session joined it. A pane tab with a live event since
    // the query began keeps that newer one.
    async function loadFacts(only?: string) {
        const since = liveSeq;
        const resp = await browser.runtime.sendMessage<RequestListTTY, ResponseListTTY>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "tty.list",
            params: undefined,
        }).catch(() => undefined);
        if (!resp || "error" in resp || !Array.isArray(resp.result?.sessions)) {
            return;
        }
        for (const session of resp.result.sessions) {
            if (typeof session?.id === "string" && hasTab(session.id)
                && (only === undefined || session.id === only) && (liveAt.get(session.id) ?? 0) <= since) {
                applyFacts(session.id, parseStatus(session.status), typeof session.title === "string" ? session.title : undefined);
            }
        }
    }

    try {
        const [activeChromeTab] = await browser.tabs.query({ active: true, windowId });
        activeChromeTabId = activeChromeTab?.id;
    } catch {
        activeChromeTabId = undefined;
    }
    void refreshStatus();

    const refreshStrip = () => {
        renderTabList(list, state!, titles, {
            onSelect: (id) => void switchTo(id),
            onClose: (id) => void closeTab(id),
        }, statuses);
        launcher.setLaunchEnabled(true);
        void refreshStatus();
    };

    function hasTab(id: string): boolean {
        return !!state && state.tabs.some((tab) => tab.id === id);
    }

    async function openKind(app: string, size: TtyDimensions, remember: boolean, cwd?: string): Promise<{ id: string; url: string; pane?: string; app: string; cwd?: string }> {
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
            // Same pane tab, new PTY session: it keeps the pane tab's grants.
            const created = await createTTY(optsFromAppId(kind, size, tabCwd(state, deadId)), tabPane(state, deadId));
            if (gen !== switchGen) {
                await destroyTTY(created.id);
                return false;
            }
            await ensureWindowAlive(windowId, created.id);
            state = replaceTabId(state, deadId, created.id, created.pane);
            statuses.delete(deadId);
            liveAt.delete(deadId);
            await saveWindowState(windowId, state);
            refreshStrip();
            void loadFacts(created.id);
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
            onReady: () => reconnect.succeeded(),
            onClose: () => {
                if (gen !== switchGen || !state) {
                    return;
                }
                if (!hasTab(attachedId) || state.activeId !== attachedId) {
                    return;
                }
                const retrying = reconnect.schedule(() => {
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
                });
                if (!retrying) {
                    showSetup(RECONNECT_GAVE_UP);
                }
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
        if (!state) {
            return;
        }
        const size = fitAddon.proposeDimensions();
        if (!size) {
            return;
        }
        const gen = ++switchGen;
        let created: { id: string; url: string; pane?: string; app: string; cwd?: string };
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
            await destroyTTY(created.id, created.pane);
            return;
        }
        state = {
            tabs: [...state.tabs, { id: created.id, app: created.app, cwd: created.cwd, pane: created.pane }],
            activeId: created.id,
        };
        await saveWindowState(windowId, state);
        refreshStrip();
        void loadFacts(created.id);
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

        // Closing the pane tab revokes its grants along with its session.
        const pane = tabPane(state, id);
        if (id !== state.activeId) {
            await destroyTTY(id, pane);
            titles.delete(id);
            hostTitled.delete(id);
            statuses.delete(id);
            liveAt.delete(id);
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
            await destroyTTY(id, pane);
            titles.delete(id);
            hostTitled.delete(id);
            statuses.delete(id);
            liveAt.delete(id);
            let created: { id: string; url: string; pane?: string };
            try {
                created = await createTTY({ mode: "shell", cols: size.cols, rows: size.rows });
                await ensureWindowAlive(windowId, created.id);
            } catch (err) {
                await clearWindowState(windowId);
                showSetup((err as Error).message);
                return;
            }
            if (gen !== switchGen) {
                await destroyTTY(created.id, created.pane);
                return;
            }
            state = { tabs: [{ id: created.id, app: "", pane: created.pane }], activeId: created.id };
            await saveWindowState(windowId, state);
            refreshStrip();
            void loadFacts(created.id);
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
            await attachActive(gen);
        } catch (err) {
            if (gen === switchGen && !(err instanceof NativeUnavailable)) {
                showSetup((err as Error).message);
            }
        } finally {
            // The pane tab is gone from state whatever happened to its
            // neighbour, so its session and grant go too.
            await destroyTTY(id, pane);
            titles.delete(id);
            hostTitled.delete(id);
            statuses.delete(id);
            liveAt.delete(id);
        }
    }

    terminal.onTitleChange((title) => {
        if (activeTtyId) {
            titles.set(activeTtyId, title);
            hostTitled.delete(activeTtyId);
            document.title = `${title}  |  Agent Pane`;
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
                await destroyTTY(created.id, created.pane);
                return;
            }
            state = { tabs: [{ id: created.id, app: "", pane: created.pane }], activeId: created.id };
            await saveWindowState(windowId, state);
            refreshStrip();
            void loadFacts(created.id);
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
        void loadFacts();
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
    const reconnect = createReconnectBackoff();
    let ephemeralUnloading = false;
    let booting = false;

    const bindSocket = (socket: WebSocket, id: string) => {
        ptyAttach?.dispose();
        ws = socket;
        activeTtyId = id;
        ptyAttach = attachPtySocket(terminal, socket, {
            isCurrent: () => !ephemeralUnloading && ws === socket,
            onReady: () => reconnect.succeeded(),
            onClose: () => {
                if (ephemeralUnloading) {
                    return;
                }
                if (!reconnect.schedule(() => recover(id))) {
                    showSetup(RECONNECT_GAVE_UP);
                }
            },
        });
    };

    const recover = (id: string) => {
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
        document.title = `${title}  |  Agent Pane`;
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
