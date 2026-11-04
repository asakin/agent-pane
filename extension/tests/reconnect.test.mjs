import assert from "node:assert/strict";
import test from "node:test";
import { createReconnectBackoff } from "../entrypoints/shared/reconnect.ts";

function fakeClock() {
    const timers = new Map();
    let next = 1;
    return {
        delays: [],
        setTimeout(fn, ms) {
            this.delays.push(ms);
            timers.set(next, fn);
            return next++;
        },
        clearTimeout(handle) {
            timers.delete(handle);
        },
        fire() {
            const [[handle, fn]] = timers;
            timers.delete(handle);
            fn();
        },
        pending() { return timers.size; },
    };
}

test("each failure doubles the wait, up to the cap", () => {
    const clock = fakeClock();
    const backoff = createReconnectBackoff({ baseMs: 100, maxMs: 500, maxAttempts: 10, clock });
    for (let i = 0; i < 5; i++) {
        assert.equal(backoff.schedule(() => {}), true);
        clock.fire();
    }
    assert.deepEqual(clock.delays, [100, 200, 400, 500, 500]);
});

test("it gives up after repeated refusals instead of looping", () => {
    const clock = fakeClock();
    const backoff = createReconnectBackoff({ maxAttempts: 3, clock });
    let runs = 0;
    for (let i = 0; i < 3; i++) {
        assert.equal(backoff.schedule(() => runs++), true);
        clock.fire();
    }
    assert.equal(backoff.schedule(() => runs++), false);
    assert.equal(runs, 3);
    assert.equal(clock.pending(), 0);
});

test("a socket that got its replay resets the count", () => {
    const clock = fakeClock();
    const backoff = createReconnectBackoff({ baseMs: 100, maxAttempts: 2, clock });
    backoff.schedule(() => {});
    clock.fire();
    backoff.schedule(() => {});
    clock.fire();
    backoff.succeeded();
    assert.equal(backoff.schedule(() => {}), true);
    assert.equal(clock.delays.at(-1), 100);
});

test("one retry is pending at a time", () => {
    const clock = fakeClock();
    const backoff = createReconnectBackoff({ clock });
    let runs = 0;
    backoff.schedule(() => runs++);
    backoff.schedule(() => runs++);
    assert.equal(clock.pending(), 1);
    clock.fire();
    assert.equal(runs, 1);
    backoff.schedule(() => runs++);
    backoff.cancel();
    assert.equal(clock.pending(), 0);
});
