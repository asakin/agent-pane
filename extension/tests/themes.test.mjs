import assert from "node:assert/strict";
import test from "node:test";
import { readdirSync } from "node:fs";
import { LEGACY_THEME_DARK, THEME_CHOICES, THEME_DEFAULT, allThemeOptions, darkThemeSetting, themeOptions } from "../entrypoints/shared/themes.ts";
import { quickSettingsView } from "../entrypoints/shared/quickSettings.ts";

const hostThemes = readdirSync(new URL("../../internal/cmd/themes/", import.meta.url))
    .map((name) => name.replace(/\.json$/, ""));

test("the side panel's Settings offers five dark themes the host has, the default first", () => {
    assert.equal(THEME_CHOICES.length, 5);
    assert.equal(THEME_CHOICES[0].value, THEME_DEFAULT);
    for (const choice of THEME_CHOICES) {
        assert.ok(hostThemes.includes(choice.value), `${choice.value} is missing from internal/cmd/themes`);
    }
    assert.deepEqual(themeOptions(hostThemes, "Dracula"), [...THEME_CHOICES]);
});

test("themeOptions keeps a config.json theme outside the list and drops ones the host lacks", () => {
    const options = themeOptions(hostThemes, "Tomorrow");
    assert.equal(options.length, 6);
    assert.deepEqual(options.at(-1), { value: "Tomorrow", label: "Tomorrow" });
    assert.deepEqual(themeOptions(hostThemes, "No Such Theme"), [...THEME_CHOICES]);
    assert.deepEqual(themeOptions(["Dracula"], undefined), [{ value: "Dracula", label: "Dracula" }]);
    assert.deepEqual(themeOptions([], undefined), [{ value: THEME_DEFAULT, label: THEME_DEFAULT }]);
});

test("Options lists every theme the host has, A to Z, with the five under their short names", () => {
    const options = allThemeOptions(hostThemes, "Dracula");
    assert.equal(options.length, hostThemes.length);
    assert.deepEqual(new Set(options.map((o) => o.value)), new Set(hostThemes));
    const labels = options.map((o) => o.label);
    assert.deepEqual(labels, [...labels].sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" })));
    for (const choice of THEME_CHOICES) {
        assert.ok(options.some((o) => o.value === choice.value && o.label === choice.label));
    }
});

test("allThemeOptions keeps a config.json theme the host lacks", () => {
    assert.deepEqual(allThemeOptions(["Dracula"], "Mine"), [
        { value: "Dracula", label: "Dracula" },
        { value: "Mine", label: "Mine" },
    ]);
    assert.deepEqual(allThemeOptions([], undefined), [{ value: THEME_DEFAULT, label: THEME_DEFAULT }]);
});

test("darkThemeSetting reads themeDark, treating the legacy default as unset", () => {
    assert.equal(darkThemeSetting({ themeDark: "Dracula" }), "Dracula");
    assert.equal(darkThemeSetting({ themeDark: LEGACY_THEME_DARK }), "");
    assert.equal(darkThemeSetting({}), "");
    assert.equal(darkThemeSetting({ themeDark: 3 }), "");
});

test("the side panel's Settings shows the config.json values Options edits", () => {
    const view = quickSettingsView({
        config: { theme: "nord", themeDark: "Dracula", agentMenuAlphabetical: true },
        themes: hostThemes,
    });
    assert.equal(view.theme, "nord");
    assert.equal(view.darkTheme, "Dracula");
    assert.equal(view.alphabetical, true);
    assert.deepEqual(view.themes, [...THEME_CHOICES]);

    const custom = quickSettingsView({ config: { theme: "Tomorrow" }, themes: hostThemes });
    assert.deepEqual(custom.themes.at(-1), { value: "Tomorrow", label: "Tomorrow" });
    assert.equal(custom.alphabetical, false);

    assert.equal(quickSettingsView({ config: {}, themes: hostThemes }).theme, THEME_DEFAULT);
});
