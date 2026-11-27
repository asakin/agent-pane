import type { RequestConfigGet, RequestConfigSet, ResponseConfigGet, ResponseConfigSet } from "./rpc";
import { THEME_DEFAULT, darkThemeSetting, themeOptions } from "./themes.ts";

/**
 * What the side panel's Settings shows, from a config.get answer: the same
 * config.json keys Options edits, so the two stay in step.
 */
export type QuickSettingsView = {
    theme: string;
    themes: { value: string; label: string }[];
    /** The themeDark Options set, which replaces theme in dark mode. */
    darkTheme: string;
    alphabetical: boolean;
};

export function quickSettingsView(result: { config: Record<string, unknown>; themes?: string[] }): QuickSettingsView {
    const { config } = result;
    const theme = typeof config.theme === "string" && config.theme ? config.theme : THEME_DEFAULT;
    return {
        theme,
        themes: themeOptions(result.themes ?? [], theme),
        darkTheme: darkThemeSetting(config),
        alphabetical: config.agentMenuAlphabetical === true,
    };
}

async function readConfig(): Promise<ResponseConfigGet> {
    return browser.runtime.sendMessage<RequestConfigGet, ResponseConfigGet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.get",
        params: undefined,
    });
}

async function writeConfig(config: Record<string, unknown>): Promise<void> {
    const resp = await browser.runtime.sendMessage<RequestConfigSet, ResponseConfigSet>({
        jsonrpc: "2.0",
        id: crypto.randomUUID(),
        method: "config.set",
        params: { config },
    });
    if ("error" in resp) {
        throw new Error(resp.error.message);
    }
}

/**
 * The strip's Settings button: the everyday settings, each saved at once.
 * Everything else, and every theme, is in Options.
 */
export function mountQuickSettings(
    host: HTMLElement,
    handlers: {
        onThemeSaved: () => void | Promise<void>;
        onAlphabeticalSaved: () => void | Promise<void>;
    },
) {
    const doc = host.ownerDocument;
    host.classList.add("at-settings");
    host.replaceChildren();

    let menu: HTMLElement | null = null;

    const button = doc.createElement("button");
    button.type = "button";
    button.className = "at-settings__button";
    button.textContent = "⚙";
    button.title = "Settings";
    button.setAttribute("aria-label", "Settings");
    button.setAttribute("aria-haspopup", "dialog");
    button.setAttribute("aria-expanded", "false");
    host.appendChild(button);

    const closeMenu = () => {
        if (!menu) {
            return;
        }
        menu.remove();
        menu = null;
        button.setAttribute("aria-expanded", "false");
        doc.removeEventListener("pointerdown", onDocPointer, true);
        doc.removeEventListener("keydown", onKey, true);
    };

    const onDocPointer = (e: Event) => {
        const t = e.target as Node;
        if (host.contains(t) || menu?.contains(t)) {
            return;
        }
        closeMenu();
    };

    const onKey = (e: KeyboardEvent) => {
        if (e.key === "Escape") {
            e.preventDefault();
            closeMenu();
        }
    };

    const field = (label: string, control: HTMLElement): HTMLElement => {
        const row = doc.createElement("label");
        row.className = "at-settings__field";
        const text = doc.createElement("span");
        text.textContent = label;
        row.append(text, control);
        return row;
    };

    const render = (panel: HTMLElement, view: QuickSettingsView) => {
        const status = doc.createElement("p");
        status.className = "at-settings__note";
        status.setAttribute("role", "status");
        const fail = (err: unknown) => {
            status.textContent = err instanceof Error ? err.message : String(err);
        };

        const theme = doc.createElement("select");
        for (const choice of view.themes) {
            const opt = doc.createElement("option");
            opt.value = choice.value;
            opt.textContent = choice.label;
            theme.appendChild(opt);
        }
        theme.value = view.theme;
        theme.addEventListener("change", () => {
            status.textContent = "";
            void writeConfig({ theme: theme.value })
                .then(() => handlers.onThemeSaved())
                .catch(fail);
        });

        const alphabetical = doc.createElement("input");
        alphabetical.type = "checkbox";
        alphabetical.checked = view.alphabetical;
        alphabetical.addEventListener("change", () => {
            status.textContent = "";
            void writeConfig({ agentMenuAlphabetical: alphabetical.checked })
                .then(() => handlers.onAlphabeticalSaved())
                .catch(fail);
        });
        const check = doc.createElement("label");
        check.className = "at-settings__check";
        check.append(alphabetical, doc.createTextNode("Keep the agent menu alphabetical"));

        const rows: HTMLElement[] = [field("Theme", theme)];
        if (view.darkTheme) {
            const dark = doc.createElement("p");
            dark.className = "at-settings__note";
            dark.textContent = `In dark mode, pane tabs use ${view.darkTheme}, set in Options.`;
            rows.push(dark);
        }
        rows.push(check, status);

        const all = doc.createElement("button");
        all.type = "button";
        all.className = "at-settings__all";
        all.textContent = "All settings…";
        all.title = "Open Options: shell, environment, every theme, agents, blocked domains and more";
        all.addEventListener("click", () => {
            closeMenu();
            void browser.runtime.openOptionsPage();
        });
        rows.push(all);

        const heading = doc.createElement("div");
        heading.className = "at-settings__heading";
        heading.textContent = "Settings";
        panel.replaceChildren(heading, ...rows);
        theme.focus();
    };

    const openMenu = async () => {
        const panel = doc.createElement("div");
        panel.className = "at-settings__menu";
        panel.setAttribute("role", "dialog");
        panel.setAttribute("aria-label", "Settings");
        panel.textContent = "Loading…";
        menu = panel;
        const rect = button.getBoundingClientRect();
        panel.style.top = `${rect.bottom + 4}px`;
        panel.style.right = `${Math.max(8, doc.documentElement.clientWidth - rect.right)}px`;
        doc.body.appendChild(panel);
        button.setAttribute("aria-expanded", "true");
        doc.addEventListener("pointerdown", onDocPointer, true);
        doc.addEventListener("keydown", onKey, true);

        // Read config.json each time, so changes made in Options show here.
        const resp = await readConfig().catch((err) => err as Error);
        if (menu !== panel) {
            return;
        }
        if (resp instanceof Error || "error" in resp) {
            panel.textContent = resp instanceof Error ? resp.message : resp.error.message;
            return;
        }
        render(panel, quickSettingsView(resp.result));
    };

    button.addEventListener("click", () => {
        if (menu) {
            closeMenu();
        } else {
            void openMenu();
        }
    });
}
