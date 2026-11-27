import assert from "node:assert/strict";
import test from "node:test";
import { readdirSync } from "node:fs";
import { THEME_CHOICES, THEME_DEFAULT, themeOptions } from "../entrypoints/shared/themes.ts";

const hostThemes = readdirSync(new URL("../../internal/cmd/themes/", import.meta.url))
    .map((name) => name.replace(/\.json$/, ""));

test("Options offers five dark themes the host has, the default first", () => {
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
