import type { JSONRPCResponse } from "./rpc";

export const NATIVE_RPC_TIMEOUT_MS = 5000;
export const CHOOSE_FOLDER_TIMEOUT_MS = 120_000;

export function nativeTimeoutMs(method: string): number {
    if (method === "dialog.chooseFolder") {
        return CHOOSE_FOLDER_TIMEOUT_MS;
    }
    return NATIVE_RPC_TIMEOUT_MS;
}
export const NATIVE_TIMEOUT = -32000;
export const NATIVE_DISCONNECTED = -32001;

export class NativeUnavailable extends Error {
    constructor(message: string) {
        super(message);
        this.name = "NativeUnavailable";
    }
}

export type NativeMessagePort = {
    postMessage(message: unknown): void;
    onMessage: {
        addListener(listener: (message: unknown) => void): void;
        removeListener(listener: (message: unknown) => void): void;
    };
};

type RpcTimers = {
    setTimeout(fn: () => void, ms: number): unknown;
    clearTimeout(id: unknown): void;
};

export function isJsonRpcResponse(message: unknown): message is JSONRPCResponse {
    if (typeof message !== "object" || message === null) {
        return false;
    }
    if (!("jsonrpc" in message) || message.jsonrpc !== "2.0") {
        return false;
    }
    if (!("id" in message) || typeof message.id !== "string") {
        return false;
    }
    if ("result" in message && (typeof message.result !== "object" || message.result === null)) {
        return false;
    }
    if ("error" in message && (typeof message.error !== "object" || message.error === null)) {
        return false;
    }
    return true;
}

function rpcError(id: string, code: number, message: string): JSONRPCResponse {
    return {
        jsonrpc: "2.0",
        id,
        error: { code, message },
    };
}

export function errorFromResponse(response: {
    error?: { code: number; message: string };
}): Error | null {
    if (!response.error) {
        return null;
    }
    if (response.error.code === NATIVE_TIMEOUT || response.error.code === NATIVE_DISCONNECTED) {
        return new NativeUnavailable(response.error.message);
    }
    return new Error(response.error.message);
}

// Resolves with the matching response, or an error if the host does not answer.
// The listener is removed in either case, so a later message cannot settle it again.
export function requestNative<T extends { id?: string }>(
    port: NativeMessagePort,
    message: T,
    timeoutMs = NATIVE_RPC_TIMEOUT_MS,
    timers: RpcTimers = globalThis,
): Promise<JSONRPCResponse> {
    const id = typeof message.id === "string" ? message.id : "";
    return new Promise((resolve) => {
        let settled = false;
        const finish = (response: JSONRPCResponse) => {
            if (settled) {
                return;
            }
            settled = true;
            timers.clearTimeout(timer);
            port.onMessage.removeListener(listener);
            resolve(response);
        };
        const listener = (incoming: unknown) => {
            if (!isJsonRpcResponse(incoming) || incoming.id !== id) {
                return;
            }
            finish(incoming);
        };
        const timer = timers.setTimeout(() => {
            finish(rpcError(id, NATIVE_TIMEOUT, "Native host did not respond"));
        }, timeoutMs);
        port.onMessage.addListener(listener);
        try {
            port.postMessage(message);
        } catch {
            finish(rpcError(id, NATIVE_DISCONNECTED, "Native host is not connected"));
        }
    });
}
