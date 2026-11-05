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

export function readPage(doc: Document, url: string, format: string, full = false): PageResult {
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
