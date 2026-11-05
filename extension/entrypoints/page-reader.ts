import { readPage } from "./shared/pageContent";

// Injected with scripting.executeScript (main frame, isolated world) right
// before a read, never declared as a content script. It only defines the
// reader in the extension's isolated world; the read itself is the next
// executeScript call, whose return value is the result. No page listeners.
export default defineUnlistedScript(() => {
    (globalThis as { __agentPaneRead?: unknown }).__agentPaneRead =
        (format: string, full: boolean) => readPage(document, location.href, format, full);
});
