import assert from "node:assert/strict";
import test from "node:test";
import { parseHTML } from "linkedom";
import { renderTabList } from "../entrypoints/shared/tabStrip.ts";

const setup = () => {
    const { document } = parseHTML(`<html><body><div id="list"></div></body></html>`);
    const list = document.getElementById("list");
    const selected = [];
    const closed = [];
    const handlers = { onSelect: (id) => selected.push(id), onClose: (id) => closed.push(id) };
    return { document, list, selected, closed, handlers };
};
const state = (activeId, ...ids) => ({ tabs: ids.map((id) => ({ id, app: "" })), activeId });
const labels = (list) => Array.from(list.children).map((tab) => tab.querySelector(".at-tabs__label").textContent);
const click = (document, el) => el.dispatchEvent(new document.defaultView.Event("click", { bubbles: true }));

test("title changes keep the same pane tab buttons, so a click in progress still lands", () => {
    const { document, list, selected, handlers } = setup();
    const titles = new Map([["a", "pi"], ["b", "codex"]]);
    renderTabList(list, state("a", "a", "b"), titles, handlers);
    const [, second] = list.children;

    // A busy agent animates its title between mousedown and mouseup.
    titles.set("a", "⠋ pi working");
    renderTabList(list, state("a", "a", "b"), titles, handlers);
    titles.set("a", "⠙ pi working");
    renderTabList(list, state("a", "a", "b"), titles, handlers);

    assert.equal(list.children[1], second, "the button under the pointer is not replaced");
    assert.deepEqual(labels(list), ["⠙ pi working", "codex"]);
    assert.equal(list.children[0].title, "⠙ pi working");
    click(document, second);
    assert.deepEqual(selected, ["b"]);
});

test("the strip follows the active tab, new tabs, closed tabs and order", () => {
    const { document, list, selected, closed, handlers } = setup();
    const titles = new Map();
    renderTabList(list, state("a", "a", "b", "c"), titles, handlers);
    const [a, b, c] = list.children;
    assert.equal(a.className, "at-tabs__tab at-tabs__tab--active");
    assert.equal(b.className, "at-tabs__tab");

    renderTabList(list, state("c", "c", "a", "d"), titles, handlers);
    assert.deepEqual(Array.from(list.children).map((tab) => tab.dataset.tabId), ["c", "a", "d"]);
    assert.equal(list.children[0], c);
    assert.equal(list.children[1], a);
    assert.equal(c.className, "at-tabs__tab at-tabs__tab--active");
    assert.equal(a.className, "at-tabs__tab");
    assert.equal(b.parentNode, null, "a closed pane tab's button is removed");

    click(document, list.children[2]);
    click(document, a.querySelector(".at-tabs__close"));
    assert.deepEqual(selected, ["d"], "closing does not also select");
    assert.deepEqual(closed, ["a"]);
});

test("a label falls back to a short id and shortens long titles", () => {
    const { list, handlers } = setup();
    renderTabList(list, state("0123456789ab", "0123456789ab", "x"), new Map([["x", "a very long terminal title"]]), handlers);
    assert.deepEqual(labels(list), ["01234567", "a very long ter…"]);
    assert.equal(list.children[0].title, "0123456789ab");
});
