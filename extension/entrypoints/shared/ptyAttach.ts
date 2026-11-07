import type { Terminal } from "@xterm/xterm";

export type PtyAttachHandle = {
    dispose: () => void;
    /**
     * Sends input to the session unless the attachment is not taking input
     * (replay still running, superseded, or socket closed). Reports whether
     * it was sent.
     */
    sendInput: (data: string) => boolean;
};

/**
 * Attach a PTY WebSocket to xterm with scrollback-safe input gating.
 *
 * The host always sends one binary replay frame first (possibly empty). We
 * suppress onData/onBinary until that frame's terminal.write callback runs so
 * Device Attribute replies from re-parsed CSI queries never hit the shell.
 */
export function attachPtySocket(
    terminal: Terminal,
    socket: WebSocket,
    options?: {
        /** Return false to ignore late write callbacks after tab switch / detach. */
        isCurrent?: () => boolean;
        /** The replay frame arrived: the host accepted this socket. */
        onReady?: () => void;
        /** Called once, on the socket's first error or close event. */
        onClose?: () => void;
    },
): PtyAttachHandle {
    socket.binaryType = "arraybuffer";

    let disposed = false;
    let acceptInput = false;
    let sawFirstBinary = false;
    const disposables: { dispose: () => void }[] = [];

    const alive = () => !disposed && (options?.isCurrent?.() ?? true);

    const enableInput = () => {
        if (!alive()) {
            return;
        }
        acceptInput = true;
    };

    // Host accepts binary frames only (text is ignored). onData is UTF-8 text;
    // onBinary is a byte string (each char = one octet).
    const encoder = new TextEncoder();

    const send = (data: string): boolean => {
        if (!acceptInput || !alive() || socket.readyState !== WebSocket.OPEN) {
            return false;
        }
        socket.send(encoder.encode(data));
        return true;
    };

    const sendBinary = (data: string) => {
        if (!acceptInput || !alive() || socket.readyState !== WebSocket.OPEN) {
            return;
        }
        const buf = new Uint8Array(data.length);
        for (let i = 0; i < data.length; i++) {
            buf[i] = data.charCodeAt(i) & 0xff;
        }
        socket.send(buf);
    };

    disposables.push(terminal.onData((data) => void send(data)));
    disposables.push(terminal.onBinary(sendBinary));

    const onMessage = (ev: MessageEvent) => {
        if (disposed || typeof ev.data === "string") {
            return;
        }
        if (!(ev.data instanceof ArrayBuffer)) {
            return;
        }
        const bytes = new Uint8Array(ev.data);
        if (!sawFirstBinary) {
            sawFirstBinary = true;
            options?.onReady?.();
            if (bytes.byteLength === 0) {
                enableInput();
                return;
            }
            terminal.write(bytes, () => {
                enableInput();
            });
            return;
        }
        terminal.write(bytes);
    };

    // A failed socket fires error and then close; reattach once, not twice,
    // or the two attaches supersede each other's tokens.
    let closed = false;
    const onClose = () => {
        if (disposed || closed) {
            return;
        }
        closed = true;
        options?.onClose?.();
    };

    socket.addEventListener("message", onMessage);
    socket.addEventListener("close", onClose);
    socket.addEventListener("error", onClose);

    return {
        sendInput: send,
        dispose: () => {
            if (disposed) {
                return;
            }
            disposed = true;
            acceptInput = false;
            for (const d of disposables) {
                try {
                    d.dispose();
                } catch {
                    // already disposed
                }
            }
            disposables.length = 0;
            socket.removeEventListener("message", onMessage);
            socket.removeEventListener("close", onClose);
            socket.removeEventListener("error", onClose);
        },
    };
}
