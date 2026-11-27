/** A terminal color theme a picker offers: `value` names a file in the host's themes. */
export interface ThemeChoice {
    value: string;
    label: string;
}

export const THEME_DEFAULT = "Tomorrow Night";

/** Popular dark themes, the default first: the side panel's Settings picker. */
export const THEME_CHOICES: readonly ThemeChoice[] = [
    { value: THEME_DEFAULT, label: "Tomorrow Night" },
    { value: "Dracula", label: "Dracula" },
    { value: "Builtin Solarized Dark", label: "Solarized Dark" },
    { value: "nord", label: "Nord" },
    { value: "GruvboxDark", label: "Gruvbox Dark" },
];

/**
 * The themes the side panel's Settings offers: the choices the host has, and the current theme when
 * config.json names another one the host has, so saving keeps it.
 */
export function themeOptions(available: readonly string[], current: unknown): ThemeChoice[] {
    const have = new Set(available);
    const options = THEME_CHOICES.filter((c) => have.has(c.value));
    if (typeof current === "string" && have.has(current) && !options.some((c) => c.value === current)) {
        options.push({ value: current, label: current });
    }
    if (options.length === 0) {
        options.push({ value: THEME_DEFAULT, label: THEME_DEFAULT });
    }
    return options;
}

/** The themeDark earlier versions wrote into every new config.json; the host drops it when a theme is saved. */
export const LEGACY_THEME_DARK = "Tomorrow Night";

function themeLabel(value: string): string {
    return THEME_CHOICES.find((c) => c.value === value)?.label ?? value;
}

/**
 * Every theme the host has, for Options, A to Z by label, with the current
 * theme kept when the host lacks it, so saving keeps it.
 */
export function allThemeOptions(available: readonly string[], current: unknown): ThemeChoice[] {
    const values = new Set(available);
    if (typeof current === "string" && current) {
        values.add(current);
    }
    if (values.size === 0) {
        values.add(THEME_DEFAULT);
    }
    return [...values]
        .map((value) => ({ value, label: themeLabel(value) }))
        .sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: "base" }));
}

/**
 * The themeDark config.json sets, or "" when dark mode uses theme. The legacy
 * value counts as unset, as it does once the host saves a theme.
 */
export function darkThemeSetting(config: Record<string, unknown>): string {
    const dark = config.themeDark;
    return typeof dark === "string" && dark !== LEGACY_THEME_DARK ? dark : "";
}
