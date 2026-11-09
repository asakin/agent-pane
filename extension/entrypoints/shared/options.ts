import type {
    AgentInfo,
    RequestConfigGet,
    RequestConfigSet,
    ResponseConfigGet,
    ResponseConfigSet,
} from "./rpc";
import { clampFontSize } from "./fontSize";
import { normalizeTerminalBackend } from "./terminalBackend";
import {
    BLOCKED_DOMAINS_KEY,
    type BlockedDomainEdit,
    blockedDomainsFromConfig,
    createBlockedDomainEditor,
    normalizeBlockedDomains,
    parseBlockedDomain,
} from "./blockedDomains";

const form = document.getElementById("settings-form") as HTMLFormElement;
const statusEl = document.getElementById("status") as HTMLDivElement;
const configPathEl = document.getElementById("config-path") as HTMLParagraphElement;
const commandEl = document.getElementById("command") as HTMLInputElement;
const argsEl = document.getElementById("args") as HTMLInputElement;
const themeEl = document.getElementById("theme") as HTMLSelectElement;
const terminalBackendEl = document.getElementById("terminalBackend") as HTMLSelectElement;
const fontSizeEl = document.getElementById("fontSize") as HTMLInputElement;
const envEl = document.getElementById("env") as HTMLTextAreaElement;
const cwdEl = document.getElementById("cwd") as HTMLInputElement;
const agentsEl = document.getElementById("agents") as HTMLDivElement;
const alphabeticalEl = document.getElementById("agentMenuAlphabetical") as HTMLInputElement;
const blockedInputEl = document.getElementById("blockedDomainInput") as HTMLInputElement;
const blockedAddEl = document.getElementById("blockedDomainAdd") as HTMLButtonElement;
const blockedListEl = document.getElementById("blockedDomains") as HTMLUListElement;

let loadedConfig: Record<string, unknown> = {};
let knownAgents: AgentInfo[] = [];

function setStatus(message: string, kind: "ok" | "error" | "" = "") {
    statusEl.hidden = !message;
    statusEl.textContent = message;
    statusEl.className = "at-status" + (kind ? ` at-status--${kind}` : "");
}

function envToText(env: unknown): string {
    if (!env || typeof env !== "object") {
        return "";
    }
    return Object.entries(env as Record<string, unknown>)
        .map(([k, v]) => `${k}=${String(v ?? "")}`)
        .join("\n");
}

function textToEnv(text: string): Record<string, string> {
    const out: Record<string, string> = {};
    for (const line of text.split("\n")) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith("#")) {
            continue;
        }
        const eq = trimmed.indexOf("=");
        if (eq <= 0) {
            continue;
        }
        out[trimmed.slice(0, eq)] = trimmed.slice(eq + 1);
    }
    return out;
}

function fillSelect(select: HTMLSelectElement, values: string[], selected?: string) {
    select.innerHTML = "";
    for (const value of values) {
        const opt = document.createElement("option");
        opt.value = value;
        opt.textContent = value;
        select.appendChild(opt);
    }
    if (selected !== undefined && values.includes(selected)) {
        select.value = selected;
    } else if (values.length > 0) {
        select.value = values[0]!;
    }
}

function renderAgents(agents: AgentInfo[]) {
    agentsEl.replaceChildren();
    const sorted = [...agents].sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }));
    for (const agent of sorted) {
        const row = document.createElement("label");
        row.className = "at-agent";
        const box = document.createElement("input");
        box.type = "checkbox";
        box.checked = agent.enabled;
        box.dataset.id = agent.id;
        row.appendChild(box);
        const name = document.createElement("span");
        name.textContent = agent.name || agent.id;
        row.appendChild(name);
        if (!agent.available) {
            const note = document.createElement("span");
            note.className = "at-agent__missing";
            note.textContent = "not on PATH";
            row.appendChild(note);
        }
        agentsEl.appendChild(row);
    }
}

function agentOverrides(): Record<string, unknown> {
    const current = loadedConfig.agents;
    const out: Record<string, unknown> = current && typeof current === "object"
        ? { ...(current as Record<string, unknown>) }
        : {};
    for (const box of agentsEl.querySelectorAll<HTMLInputElement>("input[type=checkbox][data-id]")) {
        const id = box.dataset.id;
        if (!id) {
            continue;
        }
        const prev = out[id];
        const entry: Record<string, unknown> = prev && typeof prev === "object"
            ? { ...(prev as Record<string, unknown>) }
            : {};
        entry.enabled = box.checked;
        out[id] = entry;
    }
    return out;
}

function renderBlockedDomains(domains: string[]) {
    blockedListEl.replaceChildren();
    if (domains.length === 0) {
        const empty = document.createElement("li");
        empty.textContent = "No blocked domains.";
        blockedListEl.appendChild(empty);
        return;
    }
    for (const domain of domains) {
        const row = document.createElement("li");
        const name = document.createElement("span");
        name.textContent = domain;
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "at-btn";
        remove.textContent = "Remove";
        remove.setAttribute("aria-label", `Remove ${domain}`);
        remove.addEventListener("click", () => void editBlockedDomains({ remove: domain }, `Removed ${domain}.`));
        row.append(name, remove);
        blockedListEl.appendChild(row);
    }
}

// Each edit reads the stored list, so edits from this page made before an
// earlier save answered, and edits from another Options page, are kept.
const blockedDomainEditor = createBlockedDomainEditor({
    load: async () => blockedDomainsFromConfig(await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    })),
    save: async (domains) => {
        const resp = await browser.runtime.sendMessage<RequestConfigSet, ResponseConfigSet>({
            jsonrpc: "2.0",
            id: crypto.randomUUID(),
            method: "config.set",
            params: { config: { [BLOCKED_DOMAINS_KEY]: domains } },
        });
        if ("error" in resp) {
            throw new Error(resp.error.message);
        }
        loadedConfig = { ...loadedConfig, ...resp.result.config };
        renderBlockedDomains(normalizeBlockedDomains(loadedConfig[BLOCKED_DOMAINS_KEY]));
    },
});

// The list saves on its own, at once, so a new block applies to reads
// without waiting for the form's Save.
async function editBlockedDomains(edit: BlockedDomainEdit, done: string) {
    setStatus("Saving…");
    try {
        await blockedDomainEditor(edit);
    } catch (err) {
        setStatus(err instanceof Error ? err.message : String(err), "error");
        return;
    }
    setStatus(done, "ok");
}

function addBlockedDomain() {
    const parsed = parseBlockedDomain(blockedInputEl.value);
    if ("error" in parsed) {
        setStatus(parsed.error, "error");
        return;
    }
    blockedInputEl.value = "";
    void editBlockedDomains({ add: parsed.domain }, `Blocked ${parsed.domain} and its subdomains.`);
}

function parseArgs(raw: string): string[] {
    return raw
        .trim()
        .split(/\s+/)
        .filter(Boolean);
}

async function load() {
    setStatus("Loading…");
    const resp = await browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });

    if ("error" in resp) {
        setStatus(resp.error.message, "error");
        return;
    }

    const { config, themes, path, agents } = resp.result;
    loadedConfig = config;
    knownAgents = Array.isArray(agents) ? agents : [];
    configPathEl.textContent = path;

    const themeList = themes?.length ? themes : ["Tomorrow Night"];
    fillSelect(themeEl, themeList, String(config.theme ?? "Tomorrow Night"));

    commandEl.value = String(config.command ?? "");
    const args = Array.isArray(config.args) ? (config.args as string[]) : [];
    argsEl.value = args.join(" ");

    const xterm = (config.xterm && typeof config.xterm === "object")
        ? config.xterm as Record<string, unknown>
        : {};
    fontSizeEl.value = String(clampFontSize(xterm.fontSize ?? 13));
    terminalBackendEl.value = normalizeTerminalBackend(config.terminalBackend);
    envEl.value = envToText(config.env);
    cwdEl.value = typeof config.cwd === "string" ? config.cwd : "";
    alphabeticalEl.checked = config.agentMenuAlphabetical === true;
    renderAgents(knownAgents);
    renderBlockedDomains(normalizeBlockedDomains(config[BLOCKED_DOMAINS_KEY]));
    blockedInputEl.disabled = false;
    blockedAddEl.disabled = false;
    setStatus("Loaded.", "ok");
}

async function save(event: Event) {
    event.preventDefault();
    setStatus("Saving…");

    const config: Record<string, unknown> = {
        command: commandEl.value.trim() || undefined,
        args: parseArgs(argsEl.value),
        theme: themeEl.value,
        env: textToEnv(envEl.value),
        cwd: cwdEl.value.trim() || null,
        xterm: {
            fontSize: clampFontSize(fontSizeEl.value),
        },
        agents: agentOverrides(),
        agentMenuAlphabetical: alphabeticalEl.checked,
        terminalBackend: normalizeTerminalBackend(terminalBackendEl.value),
    };

    const resp = await browser.runtime.sendMessage<RequestConfigSet, ResponseConfigSet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.set",
        params: { config },
    });

    if ("error" in resp) {
        setStatus(resp.error.message, "error");
        return;
    }

    loadedConfig = { ...loadedConfig, ...resp.result.config };
    setStatus("Saved. Open a new side-panel session to apply.", "ok");
}

form.addEventListener("submit", (e) => void save(e));
blockedAddEl.addEventListener("click", addBlockedDomain);
blockedInputEl.addEventListener("keydown", (e) => {
    // Enter adds the entry instead of submitting the form.
    if (e.key === "Enter") {
        e.preventDefault();
        addBlockedDomain();
    }
});

void load();
