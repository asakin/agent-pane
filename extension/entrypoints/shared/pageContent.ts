/**
 * What the injected reader returns for each format. Runs inside the tab, in
 * the isolated world, on a clone of the document, so the page is not changed.
 * Also runs under Node in the tests, on a parsed document.
 */
import { Readability } from "@mozilla/readability";
import TurndownService from "turndown";

export const PAGE_FORMATS = ["info", "selection", "text", "dom", "markdown"] as const;
export type PageFormat = typeof PAGE_FORMATS[number];

export type PageResult = { content: string } | { error: string };

/** Readability "succeeded" on a fragment of a bigger page. */
export function isThin(bodyChars: number, articleChars: number): boolean {
    return bodyChars > 500 && articleChars < bodyChars * 0.15;
}

function visibleText(node: Element | null | undefined): string {
    if (!node) {
        return "";
    }
    const inner = (node as HTMLElement).innerText;
    return typeof inner === "string" ? inner : node.textContent ?? "";
}

/** Readability's article when it is usable, else null. */
function mainArticle(doc: Document): { title: string; content: string } | null {
    const bodyChars = visibleText(doc.body).trim().length;
    let article: { title?: string | null; content?: string | null; textContent?: string | null } | null = null;
    try {
        article = new Readability(doc.cloneNode(true) as Document).parse();
    } catch {
        return null;
    }
    if (!article?.content) {
        return null;
    }
    const articleChars = (article.textContent ?? "").trim().length;
    if (isThin(bodyChars, articleChars)) {
        return null;
    }
    return { title: article.title ?? "", content: article.content };
}

/**
 * The document's base URL: its first <base href>, resolved against the
 * document's own URL, or that URL.
 */
export function effectiveBase(doc: Document, url: string): string {
    const href = doc.querySelector("base[href]")?.getAttribute("href");
    if (href) {
        try {
            return new URL(href, url).href;
        } catch {
            // An unusable <base> is ignored, as browsers do.
        }
    }
    return url;
}

/**
 * Rewrites link and image targets under root as absolute URLs, so the
 * Markdown still names the same resources away from the page.
 */
function absolutizeTargets(root: ParentNode, base: string) {
    for (const [selector, attr] of [["a[href]", "href"], ["img[src]", "src"]] as const) {
        for (const el of Array.from(root.querySelectorAll(selector))) {
            const value = el.getAttribute(attr) ?? "";
            if (value.startsWith("#") && attr === "href") {
                continue;
            }
            try {
                el.setAttribute(attr, new URL(value, base).href);
            } catch {
                // Leave targets that are not URLs alone.
            }
        }
    }
}

/** URLs naming the same document: fragments do not change it. */
export function sameDocument(a: string, b: string): boolean {
    return a.split("#")[0] === b.split("#")[0];
}

export function toMarkdown(doc: Document, url: string, full: boolean): string {
    const article = full ? null : mainArticle(doc);
    const clone = doc.cloneNode(true) as Document;
    let root: Node;
    let title = doc.title;
    if (article) {
        const holder = clone.createElement("div");
        holder.innerHTML = article.content;
        root = holder;
        title = article.title || title;
    } else {
        root = clone.body ?? clone.documentElement;
    }
    absolutizeTargets(root as ParentNode, effectiveBase(doc, url));
    const turndown = new TurndownService({
        headingStyle: "atx",
        codeBlockStyle: "fenced",
        bulletListMarker: "-",
    });
    turndown.remove(["script", "style", "noscript", "template"]);
    const markdown = turndown.turndown(root as HTMLElement).replace(/\n{3,}/g, "\n\n").trim();
    return `# ${title.trim()}\n${url}\n\n${markdown}\n`;
}

function selectedText(doc: Document): string {
    const active = doc.activeElement as HTMLInputElement | HTMLTextAreaElement | null;
    if (active && (active.tagName === "TEXTAREA" || active.tagName === "INPUT")) {
        try {
            const { selectionStart, selectionEnd, value } = active;
            if (selectionStart != null && selectionEnd != null && selectionEnd > selectionStart) {
                return value.slice(selectionStart, selectionEnd);
            }
        } catch {
            // Inputs without a selection API (checkbox, ...) throw.
        }
    }
    return doc.defaultView?.getSelection?.()?.toString() ?? "";
}

export function readPage(doc: Document, url: string, format: string, full = false, expectUrl?: string): PageResult {
    if (expectUrl !== undefined && !sameDocument(url, expectUrl)) {
        return { error: "the tab moved to another page after the read was approved; run the command again" };
    }
    if (doc.contentType === "application/pdf") {
        return { error: "Chrome's built-in PDF viewer cannot be read by extensions" };
    }
    switch (format) {
        case "info":
            return { content: `${url}\n${doc.title}` };
        case "selection":
            return { content: selectedText(doc) };
        case "text":
            return { content: visibleText(doc.body) };
        case "dom":
            return { content: doc.documentElement?.outerHTML ?? "" };
        case "markdown":
            return { content: toMarkdown(doc, url, full) };
        default:
            return { error: `unknown format ${JSON.stringify(format)}` };
    }
}
