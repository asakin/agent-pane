export const FONT_SIZE_MIN = 10;
export const FONT_SIZE_MAX = 28;
export const FONT_SIZE_DEFAULT = 13;

export function clampFontSize(raw: unknown): number {
    const n = typeof raw === "number" ? raw : Number(raw);
    if (!Number.isFinite(n)) {
        return FONT_SIZE_DEFAULT;
    }
    return Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, Math.round(n)));
}

/** Move one pixel per step, staying inside the allowed range. */
export function stepFontSize(current: unknown, delta: number): number {
    return clampFontSize(clampFontSize(current) + Math.sign(delta));
}

export function canStepFontSize(current: unknown, delta: number): boolean {
    return stepFontSize(current, delta) !== clampFontSize(current);
}

/** config.set replaces top-level keys, so keep the other xterm options. */
export function configWithFontSize(config: Record<string, unknown>, size: number): Record<string, unknown> {
    const xterm = config.xterm && typeof config.xterm === "object"
        ? config.xterm as Record<string, unknown>
        : {};
    return { xterm: { ...xterm, fontSize: clampFontSize(size) } };
}
