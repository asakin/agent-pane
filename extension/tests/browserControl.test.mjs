import assert from "node:assert/strict";
import test from "node:test";
import { BROWSER_HOST_OUTDATED, browserReportFromResponse, browserStatusLabel } from "../entrypoints/shared/browserControl.ts";

function answer(result) {
    return { jsonrpc: "2.0", id: "1", result };
}

test("the host's report is shown as it came", () => {
    const got = browserReportFromResponse(answer({
        note: "Using one is your choice and your responsibility.",
        items: [
            { id: "playwriter-cli", name: "Playwriter CLI", status: "missing", detail: "Not installed", commands: ["npm i -g playwriter"] },
            { id: "playwriter-extension", name: "Playwriter extension", status: "action", detail: "", url: "https://chromewebstore.google.com/detail/playwriter/jfeammnjpkecdekppnclgkkffahnhfhe", urlLabel: "Open in Chrome Web Store" },
        ],
        nextSteps: ["Install the Playwriter CLI"],
    }));
    assert.ok("report" in got);
    assert.equal(got.report.items.length, 2);
    assert.deepEqual(got.report.items[0].commands, ["npm i -g playwriter"]);
    assert.equal(got.report.items[1].urlLabel, "Open in Chrome Web Store");
    assert.deepEqual(got.report.nextSteps, ["Install the Playwriter CLI"]);
});

test("only https links are offered", () => {
    const got = browserReportFromResponse(answer({
        items: [
            { name: "A", status: "action", url: "javascript:alert(1)", urlLabel: "Open" },
            { name: "B", status: "weird", url: "chrome://settings" },
        ],
    }));
    assert.equal(got.report.items[0].url, undefined);
    assert.equal(got.report.items[0].urlLabel, undefined);
    assert.equal(got.report.items[1].url, undefined);
    assert.equal(got.report.items[1].status, "action");
});

test("a host without browser.status asks for an update", () => {
    assert.deepEqual(
        browserReportFromResponse({ jsonrpc: "2.0", id: "1", error: { code: -32601, message: "Method not found" } }),
        { error: BROWSER_HOST_OUTDATED },
    );
    assert.deepEqual(
        browserReportFromResponse({ jsonrpc: "2.0", id: "1", error: { code: -32000, message: "Native host did not respond" } }),
        { error: "Native host did not respond" },
    );
    assert.ok("error" in browserReportFromResponse(undefined));
});

test("each state has a label", () => {
    assert.equal(browserStatusLabel("found"), "Found");
    assert.equal(browserStatusLabel("missing"), "Not found");
    assert.equal(browserStatusLabel("outdated"), "Needs a newer Node.js");
    assert.equal(browserStatusLabel("action"), "Set up yourself");
});
