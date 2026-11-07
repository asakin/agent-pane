/**
 * Blocked domains: hostnames whose pages Agent Pane never reads, for agents
 * or for Send tab, whatever a pane tab has approved. The list lives in the
 * host's config.json as `blockedDomains` and is read again at each check, so
 * a change applies to reads already under way. No browser APIs here: the
 * injected reader and the tests use it too.
 */

export const BLOCKED_DOMAINS_KEY = "blockedDomains";

/** A hostname in one form: lower case, no trailing dots. */
function normalHost(host: string): string {
    return host.toLowerCase().replace(/\.+$/, "");
}

/**
 * The hostname an entry names, or an error saying why it is not one. An
 * entry is a hostname, optionally with a port, or a URL whose hostname is
 * used; ports, paths and case do not matter.
 */
export function parseBlockedDomain(entry: string): { domain: string } | { error: string } {
    const raw = entry.trim();
    if (!raw) {
        return { error: "Enter a domain, such as bank.example" };
    }
    if (/[\s*]/.test(raw)) {
        return { error: "Enter one domain without spaces or wildcards; subdomains are included already" };
    }
    const isUrl = /^[a-z][a-z0-9+.-]*:\/\//i.test(raw);
    let u: URL;
    try {
        u = new URL(isUrl ? raw : `http://${raw}`);
    } catch {
        return { error: `${raw} is not a domain` };
    }
    if (!isUrl && (u.pathname !== "/" || u.search || u.hash || u.username || u.password || raw.includes("/"))) {
        return { error: "Enter only the domain, such as bank.example, or a full URL" };
    }
    const domain = normalHost(u.hostname);
    if (!domain || domain.startsWith(".") || domain.includes("..")) {
        return { error: `${raw} is not a domain` };
    }
    return { domain };
}

/** The usable, distinct hostnames in a stored list; anything else is dropped. */
export function normalizeBlockedDomains(list: unknown): string[] {
    if (!Array.isArray(list)) {
        return [];
    }
    const out: string[] = [];
    for (const entry of list) {
        if (typeof entry !== "string") {
            continue;
        }
        const parsed = parseBlockedDomain(entry);
        if ("domain" in parsed && !out.includes(parsed.domain)) {
            out.push(parsed.domain);
        }
    }
    return out;
}

/**
 * The entry that blocks url, or null. An entry matches its own hostname and
 * every subdomain: bank.example blocks login.bank.example, not
 * notbank.example or bank.example.evil.test.
 */
export function blockingDomain(url: string, blocked: readonly string[]): string | null {
    let host: string;
    try {
        host = normalHost(new URL(url).hostname);
    } catch {
        return null;
    }
    if (!host) {
        return null;
    }
    for (const entry of blocked) {
        const domain = normalHost(entry);
        if (domain && (host === domain || host.endsWith(`.${domain}`))) {
            return domain;
        }
    }
    return null;
}

/** What a blocked read reports. It names the entry, nothing from the page. */
export function blockedError(domain: string): string {
    return `blocked: the page's domain matches ${domain} in Agent Pane's Blocked domains`;
}

/** Throws the blocked error when any of urls is blocked. */
export function assertNotBlocked(urls: readonly (string | undefined)[], blocked: readonly string[]) {
    for (const url of urls) {
        const domain = url ? blockingDomain(url, blocked) : null;
        if (domain) {
            throw new Error(blockedError(domain));
        }
    }
}

/**
 * The current list from a `config.get` answer. A failed answer is an error,
 * so a read never goes ahead without knowing the policy.
 */
export function blockedDomainsFromConfig(resp: unknown): string[] {
    const r = resp as { result?: { config?: Record<string, unknown> }; error?: { message?: string } } | undefined;
    const config = r?.result?.config;
    if (!config || typeof config !== "object") {
        const why = r?.error?.message;
        throw new Error(`could not check Agent Pane's Blocked domains${why ? `: ${why}` : ""}`);
    }
    return normalizeBlockedDomains(config[BLOCKED_DOMAINS_KEY]);
}
