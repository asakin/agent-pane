/**
 * What the program in a pane tab reports it is doing. The host sends it as
 * `tty.event` for pane tabs on the Durable (preview) terminal backend, and
 * `tty.list` carries the latest; Built-in pane tabs report none.
 */
export type PaneStatus = {
    state: "idle" | "working" | "done" | "blocked" | "error";
    msg?: string;
    /** Why a blocked program needs a person: permission, question or auth. */
    kind?: string;
};

/** The `tty.event` notification's params. */
export type TtyEvent = {
    tty: string;
    status: PaneStatus | null;
    title?: string;
};

/** The background's relay of a `tty.event` to side panels. */
export type StatusMessage = {
    type: "agent-pane.tty-event";
    event: TtyEvent;
};

const STATES = new Set(["idle", "working", "done", "blocked", "error"]);

/** The status in a host message, or null for none or anything unknown. */
export function parseStatus(raw: unknown): PaneStatus | null {
    if (typeof raw !== "object" || raw === null) {
        return null;
    }
    const r = raw as Record<string, unknown>;
    if (typeof r.state !== "string" || !STATES.has(r.state)) {
        return null;
    }
    const status: PaneStatus = { state: r.state as PaneStatus["state"] };
    if (typeof r.msg === "string" && r.msg) {
        status.msg = r.msg;
    }
    if (typeof r.kind === "string" && r.kind) {
        status.kind = r.kind;
    }
    return status;
}

/** A `tty.event` notification's params, or null when they are not one. */
export function parseTtyEvent(params: unknown): TtyEvent | null {
    const p = params as Record<string, unknown> | undefined;
    if (!p || typeof p.tty !== "string" || !p.tty) {
        return null;
    }
    const event: TtyEvent = { tty: p.tty, status: parseStatus(p.status) };
    if (typeof p.title === "string") {
        event.title = p.title;
    }
    return event;
}

export function isStatusMessage(msg: unknown): msg is StatusMessage {
    const m = msg as StatusMessage | undefined;
    return !!m && m.type === "agent-pane.tty-event" && !!m.event && typeof m.event.tty === "string";
}

const KIND_WORDS: Record<string, string> = {
    permission: "needs permission",
    question: "has a question",
    auth: "needs you to sign in",
};

/**
 * How the tab strip shows a status: a class for the mark and the words for
 * its tooltip. Null shows no mark.
 */
export function statusMark(status: PaneStatus | null | undefined): { className: string; text: string } | null {
    if (!status) {
        return null;
    }
    let text: string;
    switch (status.state) {
        case "working":
            text = "Working";
            break;
        case "blocked":
            text = `Waiting for you: ${KIND_WORDS[status.kind ?? ""] ?? "needs you"}`;
            break;
        case "done":
            text = "Done";
            break;
        case "error":
            text = "Error";
            break;
        default:
            text = "Idle";
    }
    if (status.msg) {
        text += ` (${status.msg})`;
    }
    return { className: `at-tabs__status at-tabs__status--${status.state}`, text };
}
