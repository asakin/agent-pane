/**
 * Terminal and fallback marks. Agent marks come from each agent's folder.
 */

function svg24(inner: string): string {
    return `<svg viewBox="0 0 24 24" width="16" height="16" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">${inner}</svg>`;
}

export function shellIconSVG(): string {
    return svg24(
        `<rect width="24" height="24" rx="5" fill="#1F6B5A"/>` +
        `<path d="M6.5 8.5l3.5 3.5-3.5 3.5M11.5 15.5H17.5" fill="none" stroke="#FFFFFF" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"/>`,
    );
}

export function genericIconSVG(): string {
    return svg24(
        `<rect width="24" height="24" rx="5" fill="#4B5563"/>` +
        `<path d="M7 9h10M7 12h7M7 15h8" fill="none" stroke="#FFFFFF" stroke-width="1.75" stroke-linecap="round"/>`,
    );
}

const svgDataPrefix = "data:image/svg+xml;base64,";

/** Inline an SVG data URL so currentColor and color-scheme rules still apply. */
export function iconMarkup(icon: string, shell: boolean): string {
    if (shell) {
        return shellIconSVG();
    }
    if (icon.startsWith(svgDataPrefix)) {
        try {
            const svg = atob(icon.slice(svgDataPrefix.length));
            if (svg.includes("<svg")) {
                return svg;
            }
        } catch {
            return genericIconSVG();
        }
    }
    if (icon.startsWith("data:image/")) {
        return `<img src="${icon}" width="16" height="16" alt="" />`;
    }
    return genericIconSVG();
}
