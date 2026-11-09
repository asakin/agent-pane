/** The terminal backend new pane tabs run in, as config.json's terminalBackend has it. */
export type TerminalBackend = "builtin" | "durable";

/** Built-in unless the setting names Durable (preview). */
export function normalizeTerminalBackend(raw: unknown): TerminalBackend {
    return raw === "durable" ? "durable" : "builtin";
}
