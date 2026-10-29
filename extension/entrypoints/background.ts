import { requestNative } from "~/entrypoints/shared/nativeRpc"
import type { RequestDestroyTTY } from "~/entrypoints/shared/rpc"
import {
  type WindowTabState,
  legacySessionKey,
  windowStateKey,
} from "~/entrypoints/shared/windowTabs"
import { type Browser } from 'wxt/browser';

export default defineBackground(() => {
  let _nativePort: Browser.runtime.Port | null = null;

  async function getNativePort(): Promise<Browser.runtime.Port | null> {
    if (_nativePort) {
      return _nativePort;
    }

    const port = browser.runtime.connectNative("com.agentterminal.native");

    const connected = await new Promise<boolean>((resolve) => {
      const onDisconnect = () => {
        port.onDisconnect.removeListener(onDisconnect);

        if (browser.runtime.lastError) {
          console.warn("Failed to connect:", browser.runtime.lastError.message);
          resolve(false);
        } else {
          console.warn("Disconnected from native messaging host");
          resolve(false);
        }
      };

      port.onDisconnect.addListener(onDisconnect);

      // Give the port a small window to disconnect if the host doesn't exist.
      setTimeout(() => {
        // Still connected after timeout — assume success
        resolve(true);
      }, 100); // 100ms is enough — disconnect happens almost instantly on failure
    });

    if (!connected) {
      return null;
    }

    _nativePort = port;

    _nativePort.onDisconnect.addListener(() => {
      _nativePort = null;
      if (browser.runtime.lastError) {
        console.warn("Port disconnected:", browser.runtime.lastError.message);
      } else {
        console.warn("Native messaging host disconnected");
      }
    });

    let { browserId } = await browser.storage.local.get<{ browserId?: string }>("browserId");
    if (!browserId) {
      browserId = generateSecureId(12);
      await browser.storage.local.set({ browserId });
    }

    // Handshake only. The host answers other calls without it, so a stalled
    // initialize must not hold the port.
    void initialize(_nativePort, browserId);

    return _nativePort;
  }

  function initialize(port: Browser.runtime.Port, browserId: string) {
    return requestNative(port, {
      jsonrpc: "2.0",
      method: "initialize",
      id: crypto.randomUUID(),
      params: {
        browserId,
        version: browser.runtime.getManifest().version,
      },
    });
  }

  browser.runtime.onInstalled.addListener(async () => {
    browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });
    await getNativePort();
  });

  // Ensure panel-on-click is set even if the extension was already installed.
  browser.sidePanel?.setPanelBehavior({ openPanelOnActionClick: true });

  // Panel close survives; closing the Chrome window ends that window's terminal.
  browser.windows.onRemoved.addListener((windowId) => {
    void destroyWindowSession(windowId);
  });

  async function destroyTTYViaNative(nativePort: Browser.runtime.Port, ttyId: string) {
    const msg: RequestDestroyTTY = {
      jsonrpc: "2.0",
      id: crypto.randomUUID(),
      method: "tty.destroy",
      params: { id: ttyId },
    };
    await requestNative(nativePort, msg, 2000);
  }

  async function destroyWindowSession(windowId: number) {
    const wKey = windowStateKey(windowId);
    const legacyKey = legacySessionKey(windowId);
    const stored = await browser.storage.session.get([wKey, legacyKey]);
    const state = stored[wKey] as WindowTabState | undefined;
    const legacyId = stored[legacyKey] as string | undefined;

    const ids = new Set<string>();
    const tabs = state?.tabs as unknown;
    if (Array.isArray(tabs)) {
      for (const tab of tabs) {
        if (typeof tab === "string" && tab) {
          ids.add(tab);
        } else if (tab && typeof tab === "object" && typeof (tab as { id?: unknown }).id === "string") {
          const id = (tab as { id: string }).id;
          if (id) {
            ids.add(id);
          }
        }
      }
    }
    if (typeof legacyId === "string" && legacyId) {
      ids.add(legacyId);
    }

    await browser.storage.session.remove([wKey, legacyKey]);

    if (ids.size === 0) {
      return;
    }

    const nativePort = await getNativePort();
    if (!nativePort) {
      return;
    }

    for (const ttyId of ids) {
      await destroyTTYViaNative(nativePort, ttyId);
    }
  }

  browser.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    if (sender.id !== browser.runtime.id) {
      console.warn("Received message from unknown sender:", sender.id);
      return false; // Ignore messages from unknown senders
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

    getNativePort().then(async (nativePort) => {
      if (!nativePort) {
        sendResponse({
          jsonrpc: "2.0",
          id,
          error: {
            code: -32001,
            message: "Native host is not connected",
          }
        });
        return;
      }

      const res = await requestNative(nativePort, msg);
      sendResponse(res);
    }).catch((err: unknown) => {
      sendResponse({
        jsonrpc: "2.0",
        id,
        error: {
          code: -32001,
          message: err instanceof Error ? err.message : "Native host is not connected",
        }
      });
    })

    return true
  })

  function generateSecureId(length = 12) {
    const charset = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
    const array = new Uint8Array(length);
    crypto.getRandomValues(array);
    return Array.from(array, (byte) => charset[byte % charset.length]).join('');
  }
});
