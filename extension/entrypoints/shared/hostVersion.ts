/**
 * Lowest host `protocol` this extension can drive. Raise it together with
 * `protocolVersion` in internal/cmd/serve.go when the extension starts relying
 * on something an older host does not have.
 */
export const MIN_HOST_PROTOCOL = 1;

export type HostCheck =
    | { ok: true }
    | { ok: false; version: string; protocol: number | null; needed: number };

/** Decide from the host's `initialize` result whether terminals may start. */
export function checkHost(result: unknown, needed = MIN_HOST_PROTOCOL): HostCheck {
    const info = typeof result === "object" && result !== null ? result as Record<string, unknown> : {};
    const version = typeof info.version === "string" && info.version ? info.version : "";
    const protocol = typeof info.protocol === "number" && Number.isInteger(info.protocol) ? info.protocol : null;

    // Local builds pass whatever their protocol, so development keeps working.
    if (version === "dev") {
        return { ok: true };
    }
    if (protocol !== null && protocol >= needed) {
        return { ok: true };
    }
    return { ok: false, version: version || "unknown", protocol, needed };
}
