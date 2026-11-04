/**
 * Backoff for reattaching a terminal socket after it closes. Pure apart from
 * the injected timer, so the tests drive it with a fake clock.
 *
 * The host refuses a socket whose origin or attach token it does not accept,
 * so a refusal that will never clear (a build whose origin is not on the
 * host's list) must not loop. Each failure doubles the wait; after
 * `maxAttempts` failures in a row with no replay received, it gives up.
 */

export const RECONNECT_BASE_MS = 250;
export const RECONNECT_MAX_MS = 8_000;
export const RECONNECT_MAX_ATTEMPTS = 6;

export type ReconnectClock = {
    setTimeout: (fn: () => void, ms: number) => unknown;
    clearTimeout: (handle: unknown) => void;
};

export type ReconnectBackoff = {
    /** Run `fn` after the next delay. False when it has given up instead. */
    schedule: (fn: () => void) => boolean;
    /** The socket delivered its replay: start counting from zero again. */
    succeeded: () => void;
    /** Drop a pending retry. */
    cancel: () => void;
};

export function createReconnectBackoff(options?: {
    baseMs?: number;
    maxMs?: number;
    maxAttempts?: number;
    clock?: ReconnectClock;
}): ReconnectBackoff {
    const baseMs = options?.baseMs ?? RECONNECT_BASE_MS;
    const maxMs = options?.maxMs ?? RECONNECT_MAX_MS;
    const maxAttempts = options?.maxAttempts ?? RECONNECT_MAX_ATTEMPTS;
    const clock: ReconnectClock = options?.clock ?? {
        setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
        clearTimeout: (handle) => globalThis.clearTimeout(handle as number),
    };

    let failures = 0;
    let pending: unknown = null;

    const cancel = () => {
        if (pending !== null) {
            clock.clearTimeout(pending);
            pending = null;
        }
    };

    return {
        schedule(fn) {
            cancel();
            if (failures >= maxAttempts) {
                return false;
            }
            const delay = Math.min(maxMs, baseMs * 2 ** failures);
            failures++;
            pending = clock.setTimeout(() => {
                pending = null;
                fn();
            }, delay);
            return true;
        },
        succeeded() {
            failures = 0;
        },
        cancel,
    };
}

export const RECONNECT_GAVE_UP =
    "Could not connect to the terminal. The agent-pane host refused this extension; check that the installed host matches this build, then reopen the panel.";
