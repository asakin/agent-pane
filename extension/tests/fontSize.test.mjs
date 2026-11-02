import assert from "node:assert/strict";
import test from "node:test";
import {
    FONT_SIZE_MAX,
    FONT_SIZE_MIN,
    canStepFontSize,
    clampFontSize,
    configWithFontSize,
    stepFontSize,
} from "../entrypoints/shared/fontSize.ts";

test("clampFontSize keeps sizes in 10 to 28 and defaults to 13", () => {
    assert.equal(FONT_SIZE_MIN, 10);
    assert.equal(FONT_SIZE_MAX, 28);
    assert.equal(clampFontSize(4), 10);
    assert.equal(clampFontSize(40), 28);
    assert.equal(clampFontSize("15"), 15);
    assert.equal(clampFontSize(14.6), 15);
    assert.equal(clampFontSize(undefined), 13);
    assert.equal(clampFontSize("abc"), 13);
});

test("stepFontSize moves one pixel per click and stops at the bounds", () => {
    assert.equal(stepFontSize(13, 1), 14);
    assert.equal(stepFontSize(13, -1), 12);
    assert.equal(stepFontSize(28, 1), 28);
    assert.equal(stepFontSize(10, -1), 10);
    assert.equal(stepFontSize(40, -1), 27);
    assert.equal(stepFontSize(undefined, 1), 14);
});

test("canStepFontSize disables − at 10 and + at 28", () => {
    assert.equal(canStepFontSize(10, -1), false);
    assert.equal(canStepFontSize(10, 1), true);
    assert.equal(canStepFontSize(28, 1), false);
    assert.equal(canStepFontSize(28, -1), true);
});

test("configWithFontSize keeps other xterm options", () => {
    assert.deepEqual(
        configWithFontSize({ theme: "x", xterm: { fontFamily: "Mono", fontSize: 13 } }, 16),
        { xterm: { fontFamily: "Mono", fontSize: 16 } },
    );
    assert.deepEqual(configWithFontSize({}, 99), { xterm: { fontSize: 28 } });
});
