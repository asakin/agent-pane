export const RELEASE_URL = "https://github.com/asakin/agent-pane/releases/latest";

/** The install command for this platform: install.ps1 on Windows, install.sh elsewhere. */
export function installCommand(platform = navigator.platform): string {
    return /Win/.test(platform)
        ? `irm ${RELEASE_URL}/download/install.ps1 | iex`
        : `curl -fsSL ${RELEASE_URL}/download/install.sh | sh`;
}

/**
 * Native-host disconnect screen, the first screen a new user sees: what the
 * program is for, this platform's install command with Copy, the release page,
 * and Retry.
 */
export function renderSetupScreen(errorMessage?: string, platform = navigator.platform): HTMLElement {
    const command = installCommand(platform);
    const shell = /Win/.test(platform) ? "PowerShell" : "a terminal";
    const root = document.createElement("div");
    root.className = "at-setup";
    root.innerHTML = `
      <h1 class="at-setup__title">Install the Agent Pane program</h1>
      <p class="at-setup__text">Agent Pane needs a small program on this computer. It runs the terminals you see in this panel.</p>
      <p class="at-setup__text">Run this command in ${shell}, then click Retry.</p>
      <pre class="at-setup__commands"><code>${escapeHtml(command)}</code></pre>
      <div class="at-setup__actions">
        <button type="button" class="at-btn" data-action="copy">Copy command</button>
        <button type="button" class="at-btn at-btn--primary" data-action="retry">Retry</button>
      </div>
      <p class="at-setup__text">To read the installer first, open the <a href="${RELEASE_URL}" target="_blank" rel="noopener noreferrer">latest release</a>.</p>
      ${errorMessage ? `<p class="at-setup__error">${escapeHtml(errorMessage)}</p>` : ""}
    `;

    bindCopy(root, command);
    root.querySelector('[data-action="retry"]')?.addEventListener("click", () => {
        location.reload();
    });

    return root;
}

/**
 * Host older than the extension: no terminal, only the install command and a
 * Retry that reconnects to whatever host is installed now.
 */
export function renderUpdateScreen(found: string, needed: string, onRetry: () => Promise<void>, platform = navigator.platform): HTMLElement {
    const command = installCommand(platform);
    const foundText = found ? `version <strong>${escapeHtml(found)}</strong>` : "an older version";
    const root = document.createElement("div");
    root.className = "at-setup";
    root.innerHTML = `
      <h1 class="at-setup__title">The Agent Pane program on this computer needs an update</h1>
      <p class="at-setup__text">Found ${foundText}. This extension needs version <strong>${escapeHtml(needed)}</strong> or later. Run this command, then click Retry.</p>
      <pre class="at-setup__commands"><code>${escapeHtml(command)}</code></pre>
      <div class="at-setup__actions">
        <button type="button" class="at-btn" data-action="copy">Copy command</button>
        <button type="button" class="at-btn at-btn--primary" data-action="retry">Retry</button>
      </div>
    `;

    bindCopy(root, command);

    const retry = root.querySelector<HTMLButtonElement>('[data-action="retry"]');
    retry?.addEventListener("click", () => {
        retry.disabled = true;
        onRetry().finally(() => location.reload());
    });

    return root;
}

function bindCopy(root: HTMLElement, command: string) {
    const copy = root.querySelector<HTMLButtonElement>('[data-action="copy"]');
    copy?.addEventListener("click", () => {
        navigator.clipboard.writeText(command).then(
            () => { copy.textContent = "Copied"; },
            () => { copy.textContent = "Copy failed"; },
        );
    });
}

function escapeHtml(value: string): string {
    return value
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;");
}
