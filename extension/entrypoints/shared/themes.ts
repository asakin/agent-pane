/** A terminal color theme Options offers: `value` names a file in the host's themes. */
export interface ThemeChoice {
    value: string;
    label: string;
}

export const THEME_DEFAULT = "Tomorrow Night";

/** Popular dark themes, the default first. */
export const THEME_CHOICES: readonly ThemeChoice[] = [
    { value: THEME_DEFAULT, label: "Tomorrow Night" },
    { value: "Dracula", label: "Dracula" },
    { value: "Builtin Solarized Dark", label: "Solarized Dark" },
    { value: "nord", label: "Nord" },
    { value: "GruvboxDark", label: "Gruvbox Dark" },
];

/**
 * The themes to offer: the choices the host has, and the current theme when
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
