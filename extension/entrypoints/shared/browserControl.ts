import { METHOD_NOT_FOUND } from "./nativeRpc.ts";
import type { BrowserItem, ResponseBrowserStatus } from "./rpc";

export type BrowserReport = {
    note: string;
    items: BrowserItem[];
    nextSteps: string[];
};

// The running host keeps its old binary until its connection ends, which
// reopening Options does not do; quitting Chrome does.
export const BROWSER_HOST_OUTDATED = "The Agent Pane program on this computer is older than this extension and cannot report browser control. Run the install command, then quit and reopen Chrome so the updated program starts.";

const STATUSES = new Set(["found", "missing", "broken", "outdated", "action"]);

// Only https links are opened, so a report cannot open another kind of page.
function httpsUrl(value: unknown): string | undefined {
    if (typeof value !== "string") {
        return undefined;
    }
    try {
        return new URL(value).protocol === "https:" ? value : undefined;
    } catch {
        return undefined;
    }
}

function strings(value: unknown): string[] {
    return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string") : [];
}

/** The browser.status answer as Options shows it, or the error to show. */
export function browserReportFromResponse(resp: ResponseBrowserStatus | undefined): { report: BrowserReport } | { error: string } {
    if (!resp) {
        return { error: "Agent Pane is not connected." };
    }
    if ("error" in resp) {
        return { error: resp.error.code === METHOD_NOT_FOUND ? BROWSER_HOST_OUTDATED : resp.error.message };
    }
    const raw = resp.result ?? {};
    const items: BrowserItem[] = [];
    for (const item of Array.isArray(raw.items) ? raw.items : []) {
        if (!item || typeof item !== "object" || typeof item.name !== "string") {
            continue;
        }
        const url = httpsUrl(item.url);
        items.push({
            id: typeof item.id === "string" ? item.id : "",
            name: item.name,
            optional: item.optional === true,
            status: STATUSES.has(item.status) ? item.status : "action",
            detail: typeof item.detail === "string" ? item.detail : "",
            commands: strings(item.commands),
            url,
            urlLabel: url ? (typeof item.urlLabel === "string" && item.urlLabel ? item.urlLabel : "Open") : undefined,
        });
    }
    return {
        report: {
            note: typeof raw.note === "string" ? raw.note : "",
            items,
            nextSteps: strings(raw.nextSteps),
        },
    };
}

/** The short state shown beside an item's name. */
export function browserStatusLabel(status: BrowserItem["status"]): string {
    switch (status) {
        case "found":
            return "Found";
        case "missing":
            return "Not found";
        case "broken":
            return "Found, but does not run";
        case "outdated":
            return "Needs a newer Node.js";
        default:
            return "Set up yourself";
    }
}
