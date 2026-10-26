import type { Terminal } from "@xterm/xterm";

// Match VS Code/Cursor's terminal editing sequences, plus modified Enter for agent CLIs.
export function bindKeySequences(terminal: Terminal, isMac = /Mac/.test(navigator.platform)) {
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

    terminal.attachCustomKeyEventHandler((event) => {
        // Let xterm's composition handling finish IME input before applying shortcuts.
        if (event.isComposing || event.keyCode === 229) {
            return true;
        }
        const key = event.ctrlKey && event.shiftKey && ["Digit2", "Digit6"].includes(event.code)
            ? event.code : event.key.length === 1 ? event.key.toLowerCase() : event.key;
        const combo = (event.ctrlKey ? "Ctrl+" : "") + (event.altKey ? "Alt+" : "")
            + (event.shiftKey ? "Shift+" : "") + (event.metaKey ? "Meta+" : "") + key;
        const sequence = sequences[combo];
        const action = actions[combo]
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
