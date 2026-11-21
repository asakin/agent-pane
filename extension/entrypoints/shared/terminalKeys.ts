import type { Terminal } from "@xterm/xterm";

// Match VS Code/Cursor's terminal editing sequences, plus modified Enter for agent CLIs.
// Copy and paste follow the platform's terminals: Terminal and iTerm2 on macOS,
// Windows Terminal on Windows, GNOME Terminal on Linux.
export function bindKeySequences(
    terminal: Terminal,
    platform = navigator.platform,
    writeClipboard = (text: string) => navigator.clipboard.writeText(text),
) {
    const isMac = /Mac/.test(platform);
    const isWindows = /Win/.test(platform);
    const sequences: Record<string, string> = {
        "Shift+Enter": "\x1b[13;2u",
        "Alt+ArrowUp": "\x1b[1;5A",
        "Alt+ArrowDown": "\x1b[1;5B",
        "Alt+ArrowLeft": isMac ? "\x1bb" : "\x1b[1;5D",
        "Alt+ArrowRight": isMac ? "\x1bf" : "\x1b[1;5C",
        "Ctrl+Alt+g": "\x07",
        "Ctrl+Shift+Digit2": "\x00",
        "Ctrl+Shift+Digit6": "\x1e",
        ...(isMac ? {
            "Meta+Enter": "\x1b[13;2u",
            "Shift+Meta+Enter": "\x1b[13;2u",
            "Meta+Backspace": "\x15",
            "Meta+ArrowLeft": "\x01",
            "Meta+ArrowRight": "\x05",
            "Alt+Backspace": "\x17",
            "Alt+Delete": "\x1bd",
        } : {
            "Ctrl+Backspace": "\x17",
            "Ctrl+Delete": "\x1bd",
        }),
    };
    const actions: Record<string, () => void> = isMac ? {
        "Meta+a": () => terminal.selectAll(),
        "Meta+k": () => terminal.clear(),
    } : {};
    const scrolling: Record<string, () => void> = isMac ? {
        "PageUp": () => terminal.scrollPages(-1),
        "PageDown": () => terminal.scrollPages(1),
        "Meta+Home": () => terminal.scrollToTop(),
        "Meta+End": () => terminal.scrollToBottom(),
        "Alt+Meta+PageUp": () => terminal.scrollLines(-1),
        "Alt+Meta+PageDown": () => terminal.scrollLines(1),
    } : {};

    // Copy keys copy xterm's selection, and with none either reach the program
    // (Windows Ctrl+C interrupts) or do nothing. Windows Terminal clears the
    // selection after Ctrl+C, so a second press interrupts. The selection is
    // cleared only once the copy succeeded, so a failed copy can be retried.
    const copy = (clear: boolean) => () => {
        const text = terminal.getSelection();
        writeClipboard(text).then(() => {
            if (clear && terminal.getSelection() === text) {
                terminal.clearSelection();
            }
        }, () => undefined);
    };
    // The copy key whose press copied; its repeats and keyup stay a copy even
    // after the selection is cleared, so holding Ctrl+C never interrupts.
    // Modifier presses in between do not end the hold.
    let heldCopy: string | undefined;
    const modifiers = new Set(["Control", "Shift", "Alt", "Meta"]);
    const copyKeys: Record<string, { always: boolean, clear: boolean }> = isMac ? {
        "Meta+c": { always: false, clear: false },
    } : isWindows ? {
        "Ctrl+c": { always: false, clear: true },
        "Ctrl+Shift+c": { always: true, clear: false },
    } : {
        "Ctrl+Shift+c": { always: true, clear: false },
    };
    // Paste keys are left to the browser's paste event, which xterm turns into
    // input (bracketed when the program asked for it). Without this xterm would
    // send Ctrl+V as ^V on Windows.
    const pasteKeys = new Set(isMac ? ["Meta+v"] : isWindows ? ["Ctrl+v", "Ctrl+Shift+v"] : ["Ctrl+Shift+v"]);

    terminal.attachCustomKeyEventHandler((event) => {
        // Let xterm's composition handling finish IME input before applying shortcuts.
        if (event.isComposing || event.keyCode === 229) {
            return true;
        }
        const key = event.ctrlKey && event.shiftKey && ["Digit2", "Digit6"].includes(event.code)
            ? event.code : event.key.length === 1 ? event.key.toLowerCase() : event.key;
        const combo = (event.ctrlKey ? "Ctrl+" : "") + (event.altKey ? "Alt+" : "")
            + (event.shiftKey ? "Shift+" : "") + (event.metaKey ? "Meta+" : "") + key;
        if (pasteKeys.has(combo)) {
            return false;
        }
        const copyKey = copyKeys[combo];
        const held = heldCopy === combo && (event.repeat || event.type !== "keydown");
        const copying = copyKey && (copyKey.always || held || terminal.hasSelection());
        if (event.type === "keydown" && !event.repeat && !modifiers.has(event.key)) {
            heldCopy = copying ? combo : undefined;
        }
        const sequence = sequences[combo];
        const action = (copying ? (terminal.hasSelection() ? copy(copyKey.clear) : () => undefined) : undefined)
            ?? actions[combo]
            ?? (terminal.buffer.active.type === "normal" ? scrolling[combo] : undefined)
            ?? (combo === "Escape" && terminal.hasSelection() ? () => terminal.clearSelection() : undefined);
        if (sequence === undefined && !action) {
            return true;
        }
        event.preventDefault();
        if (event.type === "keydown") {
            if (action) {
                action();
            } else {
                terminal.input(sequence!);
            }
        }
        return false;
    });
}
