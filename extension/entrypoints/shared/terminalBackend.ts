/** The terminal backend new pane tabs run in, as config.json's terminalBackend has it. */
export type TerminalBackend = "builtin" | "durable";

/** Durable unless the setting names Built-in. */
export function normalizeTerminalBackend(raw: unknown): TerminalBackend {
    return raw === "builtin" ? "builtin" : "durable";
}
