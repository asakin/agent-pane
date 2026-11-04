/**
 * Lowest host `protocol` this extension can drive. Raise it together with
 * `protocolVersion` in internal/cmd/serve.go when the extension starts relying
 * on something an older host does not have, and set `MIN_HOST_VERSION` to the
 * first release that ships it.
 */
export const MIN_HOST_PROTOCOL = 1;

/** First host release with `MIN_HOST_PROTOCOL`; shown on the update screen. */
export const MIN_HOST_VERSION = "0.3.0";

/** JSON-RPC error the background returns for terminal calls to an outdated host. */
export const HOST_OUTDATED = -32002;

export type HostCheck =
    | { ok: true }
    | { ok: false; version: string; protocol: number | null; needed: number; neededVersion: string };

/**
 * Decide from the host's `initialize` result whether terminals may start.
 * `version` is empty when the host did not report one (every release before
 * the check answers `{}`).
 */
export function checkHost(result: unknown, needed = MIN_HOST_PROTOCOL, neededVersion = MIN_HOST_VERSION): HostCheck {
    const info = typeof result === "object" && result !== null ? result as Record<string, unknown> : {};
    const version = typeof info.version === "string" ? info.version : "";
    const protocol = typeof info.protocol === "number" && Number.isInteger(info.protocol) ? info.protocol : null;

    // Local builds pass whatever their protocol, so development keeps working.
    if (version === "dev") {
        return { ok: true };
    }
    if (protocol !== null && protocol >= needed) {
        return { ok: true };
    }
    return { ok: false, version, protocol, needed, neededVersion };
}
