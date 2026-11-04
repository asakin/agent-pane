import { checkHost, HOST_OUTDATED } from "./hostVersion.ts";
import { NATIVE_DISCONNECTED, nativeTimeoutMs, requestNative, type NativeMessagePort } from "./nativeRpc.ts";
import type { JSONRPCResponse } from "./rpc.ts";

export type HostPort = NativeMessagePort & {
    onDisconnect: {
        addListener(listener: () => void): void;
        removeListener(listener: () => void): void;
    };
    disconnect(): void;
};

type Timers = {
    setTimeout(fn: () => void, ms: number): unknown;
    clearTimeout(id: unknown): void;
};

export type HostConnectionDeps = {
    connectNative(): HostPort;
    browserId(): Promise<string>;
    extensionVersion: string;
    /** `browser.runtime.lastError` message, read inside onDisconnect. */
    lastError?(): string | undefined;
    timers?: Timers;
};

/** One host process and the answer to its `initialize`. */
type Connection = {
    port: HostPort;
    handshake: Promise<JSONRPCResponse>;
};

// Terminals start only on a host that passed the handshake.
const GATED_METHODS = new Set(["tty.create", "tty.attach"]);

/**
 * The background's single native-messaging connection. Every caller shares
 * one host process: closing its port ends the process and every terminal in
 * it, so a port is only dropped when its own handshake failed.
 */
export function createHostConnection(deps: HostConnectionDeps) {
    const timers = deps.timers ?? globalThis;
    let active: Connection | null = null;
    // One connect at a time, so two callers never start two hosts.
    let pending: Promise<Connection | null> | null = null;

    function connect(): Promise<Connection | null> {
        if (!pending) {
            const attempt = open();
            pending = attempt;
            attempt.then(
                (conn) => {
                    if (!conn && pending === attempt) {
                        pending = null;
                    }
                },
                () => {
                    if (pending === attempt) {
                        pending = null;
                    }
                },
            );
        }
        return pending;
    }

    async function open(): Promise<Connection | null> {
        // Read before connecting so the port and its handshake are published together.
        const browserId = await deps.browserId();
        const port = deps.connectNative();

        const connected = await new Promise<boolean>((resolve) => {
            const onDisconnect = () => {
                port.onDisconnect.removeListener(onDisconnect);
                console.warn("Failed to connect:", deps.lastError?.() ?? "native messaging host disconnected");
                resolve(false);
            };
            port.onDisconnect.addListener(onDisconnect);
            // A missing host disconnects almost at once; still connected after this is success.
            timers.setTimeout(() => {
                port.onDisconnect.removeListener(onDisconnect);
                resolve(true);
            }, 100);
        });
        if (!connected) {
            return null;
        }

        // Handshake only. The host answers other calls without it, so a stalled
        // initialize must not hold the port.
        const conn: Connection = {
            port,
            handshake: requestNative(port, {
                jsonrpc: "2.0",
                method: "initialize",
                id: crypto.randomUUID(),
                params: { browserId, version: deps.extensionVersion },
            }, undefined, timers),
        };
        port.onDisconnect.addListener(() => {
            forget(conn);
            console.warn("Native messaging host disconnected:", deps.lastError?.() ?? "");
        });
        active = conn;
        return conn;
    }

    function forget(conn: Connection) {
        if (active === conn) {
            active = null;
            pending = null;
        }
    }

    // Only the connection whose handshake failed is closed. A stale failure
    // from a port that was already replaced must not end the replacement.
    function drop(conn: Connection) {
        if (active !== conn) {
            return;
        }
        forget(conn);
        conn.port.disconnect();
    }

    // A stalled or failed handshake must not stick to its port; the next call
    // starts the host again.
    async function handshakeOf(conn: Connection): Promise<JSONRPCResponse> {
        const res = await conn.handshake;
        if (res.error) {
            drop(conn);
        }
        return res;
    }

    /**
     * Answer the panel's `initialize`. With `reconnect` (Retry on the update
     * screen) a host that fails the check is replaced by whatever binary is
     * installed now; a host that passes, for example one another window's
     * Retry just started, is kept.
     */
    async function handshake(id: string, reconnect: boolean): Promise<JSONRPCResponse> {
        let conn = await connect();
        if (conn && reconnect) {
            const res = await handshakeOf(conn);
            if (!res.error && !checkHost(res.result).ok) {
                drop(conn);
            }
            conn = await connect();
        }
        if (!conn) {
            return notConnected(id);
        }
        return { ...await handshakeOf(conn), id };
    }

    async function forward(msg: { id: string; method?: unknown }): Promise<JSONRPCResponse> {
        const conn = await connect();
        if (!conn) {
            return notConnected(msg.id);
        }
        const method = typeof msg.method === "string" ? msg.method : "";
        if (GATED_METHODS.has(method)) {
            const res = await handshakeOf(conn);
            if (res.error) {
                return { ...res, id: msg.id };
            }
            if (!checkHost(res.result).ok) {
                return {
                    jsonrpc: "2.0",
                    id: msg.id,
                    error: {
                        code: HOST_OUTDATED,
                        message: "The Agent Pane program on this computer needs an update. Run the install command, then reopen the side panel.",
                    },
                };
            }
        }
        return requestNative(conn.port, msg, nativeTimeoutMs(method), timers);
    }

    /** Route one panel message to the host. Never rejects. */
    async function handle(msg: { id: string; method?: unknown; params?: unknown }): Promise<JSONRPCResponse> {
        try {
            if (msg.method === "initialize") {
                const params = msg.params as { reconnect?: unknown } | undefined;
                return await handshake(msg.id, params?.reconnect === true);
            }
            return await forward(msg);
        } catch (err) {
            return notConnected(msg.id, err instanceof Error ? err.message : undefined);
        }
    }

    /** The current host port, connecting if needed. */
    async function getPort(): Promise<HostPort | null> {
        return (await connect())?.port ?? null;
    }

    return { handle, getPort };
}

function notConnected(id: string, message = "Native host is not connected"): JSONRPCResponse {
    return { jsonrpc: "2.0", id, error: { code: NATIVE_DISCONNECTED, message } };
}
