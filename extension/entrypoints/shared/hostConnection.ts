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
    /**
     * Answers a request the host sends (page reads). A thrown error becomes
     * the JSON-RPC error message the host sees.
     */
    onHostRequest?(method: string, params: unknown): Promise<Record<string, unknown>>;
    /** Receives a notification the host sends (`tty.event`); nothing answers it. */
    onHostNotification?(method: string, params: unknown): void;
    timers?: Timers;
    /** Wall-clock milliseconds, as the host's `expiresAt` counts them. */
    now?(): number;
};

/**
 * Whether the host had already given up on a request when it arrived. A
 * request the host was still writing when it gave up reaches us whole, with
 * the `expiresAt` it was sent with.
 */
export function hostRequestExpired(params: unknown, now: number): boolean {
    const expiresAt = (params as { expiresAt?: unknown } | undefined)?.expiresAt;
    return typeof expiresAt === "number" && Number.isFinite(expiresAt) && expiresAt <= now;
}

/** A request from the host: a method and an id, and no result or error. */
export function isHostRequest(message: unknown): message is { jsonrpc: "2.0"; id: string; method: string; params?: unknown } {
    if (typeof message !== "object" || message === null) {
        return false;
    }
    const m = message as Record<string, unknown>;
    return m.jsonrpc === "2.0" && typeof m.id === "string" && m.id !== "" && typeof m.method === "string"
        && !("result" in m) && !("error" in m);
}

/** A notification from the host: a method and no id, so no answer. */
export function isHostNotification(message: unknown): message is { jsonrpc: "2.0"; method: string; params?: unknown } {
    if (typeof message !== "object" || message === null) {
        return false;
    }
    const m = message as Record<string, unknown>;
    return m.jsonrpc === "2.0" && !("id" in m) && typeof m.method === "string"
        && !("result" in m) && !("error" in m);
}

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
    const now = deps.now ?? Date.now;
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
        if (deps.onHostRequest) {
            const answer = deps.onHostRequest;
            port.onMessage.addListener((message) => {
                if (!isHostRequest(message)) {
                    return;
                }
                // Nobody waits for the answer, so do not act on it.
                const work = hostRequestExpired(message.params, now())
                    ? Promise.reject(new Error("expired: Agent Pane stopped waiting for this request"))
                    : answer(message.method, message.params);
                work.then(
                    (result) => ({ jsonrpc: "2.0", id: message.id, result }),
                    (err) => ({
                        jsonrpc: "2.0",
                        id: message.id,
                        error: { code: -32603, message: err instanceof Error ? err.message : String(err) },
                    }),
                ).then((reply) => {
                    try {
                        port.postMessage(reply);
                    } catch {
                        // The host is gone; its read fails on its side.
                    }
                });
            });
        }
        if (deps.onHostNotification) {
            const notified = deps.onHostNotification;
            port.onMessage.addListener((message) => {
                if (isHostNotification(message)) {
                    notified(message.method, message.params);
                }
            });
        }
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
