/**
 * Which runtime messages and ports the background answers. Pure: no browser
 * APIs, so the background and the tests share it.
 */

export type MessageSenderLike = {
    id?: string;
    url?: string;
};

/**
 * True only for this extension's own pages (side panel, options, popup,
 * terminal). A content script or injected page code reports the web page's
 * URL, so it fails the origin check even though it carries the extension id.
 *
 * `extensionOrigin` is `browser.runtime.getURL("/")`, e.g.
 * `chrome-extension://<id>/`.
 */
export function isOwnExtensionPage(
    sender: MessageSenderLike | undefined,
    runtimeId: string,
    extensionOrigin: string,
): boolean {
    if (!sender || !runtimeId || sender.id !== runtimeId) {
        return false;
    }
    if (!extensionOrigin.endsWith("/") || typeof sender.url !== "string") {
        return false;
    }
    return sender.url.startsWith(extensionOrigin);
}
