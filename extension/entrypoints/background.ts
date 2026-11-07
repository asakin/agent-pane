import { createHostConnection, type HostPort } from "~/entrypoints/shared/hostConnection"
import { requestNative } from "~/entrypoints/shared/nativeRpc"
import type { RequestDestroyTTY } from "~/entrypoints/shared/rpc"
import { isOwnExtensionPage } from "~/entrypoints/shared/sender"
import {
  type ApproveMessage,
  type CancelMessage,
  type ReadNotice,
  approvalChoice,
  approveTimeoutMs,
  readTab,
  resolveTab,
} from "~/entrypoints/shared/pageAccess"
import {
  type WindowTabState,
  legacySessionKey,
  windowStateKey,
} from "~/entrypoints/shared/windowTabs"

export default defineBackground(() => {
  const host = createHostConnection({
    connectNative: () => browser.runtime.connectNative("com.agentpane.native"),
    browserId: async () => {
      let { browserId } = await browser.storage.local.get<{ browserId?: string }>("browserId");
      if (!browserId) {
        browserId = generateSecureId(12);
        await browser.storage.local.set({ browserId });
      }
      return browserId;
    },
    extensionVersion: browser.runtime.getManifest().version,
    lastError: () => browser.runtime.lastError?.message,
    onHostRequest: answerHost,
  });

  const pageApis = {
    storageSession: browser.storage.session,
    tabs: browser.tabs,
    scripting: browser.scripting,
  };

  // Requests from the host for a terminal's `agent-pane page` command. The
  // host decides when to ask the person; the side panel holding that terminal
  // answers the prompt.
  async function answerHost(method: string, params: unknown): Promise<Record<string, unknown>> {
    const p = (params ?? {}) as Record<string, unknown>;
    const tty = typeof p.tty === "string" ? p.tty : "";
    if (!tty) {
      throw new Error("tty is required");
    }
    switch (method) {
      case "page.resolve":
        return await resolveTab(pageApis, tty);
      case "page.approve": {
        const ask: ApproveMessage = {
          type: "agent-pane.page-approve",
          id: typeof p.id === "string" ? p.id : "",
          tty,
          agent: typeof p.agent === "string" ? p.agent : "",
          tabId: Number(p.tabId),
          title: typeof p.title === "string" ? p.title : "",
          url: typeof p.url === "string" ? p.url : "",
          timeoutMs: approveTimeoutMs(Number(p.timeoutMs), p.expiresAt, Date.now()),
        };
        // No panel holding this terminal (closed, or a terminal in its own
        // window) means nobody can approve: deny.
        let answer: unknown;
        try {
          answer = await browser.runtime.sendMessage(ask);
        } catch {
          answer = undefined;
        }
        if (answer === undefined) {
          throw new Error("denied: open the Agent Pane side panel for this terminal to approve the read");
        }
        return { choice: approvalChoice(answer) };
      }
      case "page.cancel": {
        // The host stopped waiting for a prompt: take it down in the panel.
        if (typeof p.id === "string" && p.id) {
          const cancel: CancelMessage = { type: "agent-pane.page-cancel", id: p.id, tty };
          browser.runtime.sendMessage(cancel).catch(() => {});
        }
        return {};
      }
      case "page.read": {
        const tabId = Number(p.tabId);
        // Set when the read was just approved: only the document the
        // prompt named may be delivered.
        const expectUrl = typeof p.expectUrl === "string" ? p.expectUrl : undefined;
        const content = await readTab(pageApis, tabId, String(p.format ?? ""), p.full === true, expectUrl);
        const tab = await browser.tabs.get(tabId).catch(() => undefined);
        const notice: ReadNotice = {
          type: "agent-pane.page-read",
          tty,
          title: tab?.title ?? "",
          url: tab?.url ?? "",
        };
        browser.runtime.sendMessage(notice).catch(() => {});
        return { content };
      }
      default:
        throw new Error(`Method not found: ${method}`);
    }
  }

  browser.runtime.onInstalled.addListener(async () => {
    browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });
    await host.getPort();
  });

  // Ensure panel-on-click is set even if the extension was already installed.
  browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });

  // Panel close survives; closing the Chrome window ends that window's terminal.
  browser.windows.onRemoved.addListener((windowId) => {
    void destroyWindowSession(windowId);
  });

  async function destroyTTYViaNative(nativePort: HostPort, ttyId: string, pane?: string) {
    const msg: RequestDestroyTTY = {
      jsonrpc: "2.0",
      id: crypto.randomUUID(),
      method: "tty.destroy",
      params: pane ? { id: ttyId, pane } : { id: ttyId },
    };
    await requestNative(nativePort, msg, 2000);
  }

  async function destroyWindowSession(windowId: number) {
    const wKey = windowStateKey(windowId);
    const legacyKey = legacySessionKey(windowId);
    const stored = await browser.storage.session.get([wKey, legacyKey]);
    const state = stored[wKey] as WindowTabState | undefined;
    const legacyId = stored[legacyKey] as string | undefined;

    // PTY session id to its pane tab's id, when it has one; the window's
    // pane tabs close with it.
    const ids = new Map<string, string | undefined>();
    const tabs = state?.tabs as unknown;
    if (Array.isArray(tabs)) {
      for (const tab of tabs) {
        if (typeof tab === "string" && tab) {
          ids.set(tab, undefined);
        } else if (tab && typeof tab === "object" && typeof (tab as { id?: unknown }).id === "string") {
          const { id, pane } = tab as { id: string; pane?: unknown };
          if (id) {
            ids.set(id, typeof pane === "string" && pane ? pane : undefined);
          }
        }
      }
    }
    if (typeof legacyId === "string" && legacyId) {
      ids.set(legacyId, undefined);
    }

    await browser.storage.session.remove([wKey, legacyKey]);

    if (ids.size === 0) {
      return;
    }

    const nativePort = await host.getPort();
    if (!nativePort) {
      return;
    }

    for (const [ttyId, pane] of ids) {
      await destroyTTYViaNative(nativePort, ttyId, pane);
    }
  }

  const fromOwnPage = (sender: Browser.runtime.MessageSender) =>
    isOwnExtensionPage(sender, browser.runtime.id, browser.runtime.getURL("/"));

  // No port protocol yet: refuse ports from anything but this extension's pages.
  browser.runtime.onConnect.addListener((port) => {
    if (!fromOwnPage(port.sender ?? {})) {
      port.disconnect();
    }
  });

  browser.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    // Content scripts and injected page code carry this extension's id but a
    // web page's URL; only the extension's own pages may reach the host.
    if (!fromOwnPage(sender)) {
      return false;
    }

    const id = msg && typeof msg.id === "string" ? msg.id : "";
    if (!id) {
      sendResponse({
        jsonrpc: "2.0",
        error: {
          code: -32600,
          message: "id is required",
        },
      });
      return true;
    }

    host.handle({ ...msg, id }).then(sendResponse);
    return true
  })

  function generateSecureId(length = 12) {
    const charset = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
    const array = new Uint8Array(length);
    crypto.getRandomValues(array);
    return Array.from(array, (byte) => charset[byte % charset.length]).join('');
  }
});
