const SETUP_COMMANDS = `curl -fsSL https://raw.githubusercontent.com/asakin/agent-pane/main/install.sh | sh`;

/** Minimal native-host disconnect screen: error + install commands + Retry. */
export function renderSetupScreen(errorMessage?: string): HTMLElement {
    const root = document.createElement("div");
    root.className = "at-setup";
    root.innerHTML = `
      <h1 class="at-setup__title">Native host not connected</h1>
      ${errorMessage ? `<p class="at-setup__error">${escapeHtml(errorMessage)}</p>` : ""}
      <pre class="at-setup__commands"><code>${SETUP_COMMANDS}</code></pre>
      <button type="button" class="at-btn at-btn--primary" data-action="retry">Retry</button>
    `;

    root.querySelector('[data-action="retry"]')?.addEventListener("click", () => {
        location.reload();
    });

    return root;
}

/**
 * Host older than the extension: no terminal, only the install command and a
 * Retry that reconnects to whatever host is installed now.
 */
export function renderUpdateScreen(found: string, needed: number, onRetry: () => Promise<void>): HTMLElement {
    const root = document.createElement("div");
    root.className = "at-setup";
    root.innerHTML = `
      <h1 class="at-setup__title">The Agent Pane program on this computer needs an update</h1>
      <p class="at-setup__text">Found version <strong>${escapeHtml(found)}</strong>. This extension needs a version with protocol ${needed} or later. Run this command, then click Retry.</p>
      <pre class="at-setup__commands"><code>${SETUP_COMMANDS}</code></pre>
      <div class="at-setup__actions">
        <button type="button" class="at-btn" data-action="copy">Copy command</button>
        <button type="button" class="at-btn at-btn--primary" data-action="retry">Retry</button>
      </div>
    `;

    const copy = root.querySelector<HTMLButtonElement>('[data-action="copy"]');
    copy?.addEventListener("click", () => {
        navigator.clipboard.writeText(SETUP_COMMANDS).then(
            () => { copy.textContent = "Copied"; },
            () => { copy.textContent = "Copy failed"; },
        );
    });

    const retry = root.querySelector<HTMLButtonElement>('[data-action="retry"]');
    retry?.addEventListener("click", () => {
        retry.disabled = true;
        onRetry().finally(() => location.reload());
    });

    return root;
}

function escapeHtml(value: string): string {
    return value
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;");
}
