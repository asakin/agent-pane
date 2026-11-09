export type JSONRPCRequest = {
    jsonrpc: string;
    id?: string;
    method: string;
    params: Record<string, any> | any[] | undefined;
}

type JSONRPCRequestBase<M extends string, P extends Record<string, any> | undefined = undefined> = {
    jsonrpc: "2.0";
    id?: string
    method: M;
    params?: P
}

export type RequestInitialize = JSONRPCRequestBase<"initialize", {
    browserId?: string;
    version?: string;
    /** Drop the current host process and connect again (side panel Retry). */
    reconnect?: boolean;
}>

export type RequestResizeTTY = JSONRPCRequestBase<"tty.resize", {
    tty: string;
    cols: number;
    rows: number;
}>

export type RequestCreateTTY = JSONRPCRequestBase<"tty.create", {
    mode?: "app" | "shell";
    app?: string;
    args?: string[];
    cwd?: string;
    /** The pane tab a replacement session joins. */
    pane?: string;
    /** The Chrome window it is shown in, as windowName makes it. */
    window?: string;
    cols: number;
    rows: number;
}>

export type RequestAttachTTY = JSONRPCRequestBase<"tty.attach", {
    id: string;
    /** The Chrome window it is now shown in, as windowName makes it. */
    window?: string;
}>

/**
 * The Chrome window showing a pane tab closed. A Durable session keeps
 * running with its grants revoked; a Built-in one ends with its pane tab.
 */
export type RequestDetachTTY = JSONRPCRequestBase<"tty.detach", {
    id: string;
    pane?: string;
}>

export type ResponseDetachTTY = JSONRPCResponseBase<{
    /** Whether the session still runs. */
    running: boolean;
}>

export type RequestListTTY = JSONRPCRequestBase<"tty.list", undefined>

export type RequestDestroyTTY = JSONRPCRequestBase<"tty.destroy", {
    id: string;
    /** Set when the pane tab is closing: revokes its grants. */
    pane?: string;
}>

export type RequestChooseFolder = JSONRPCRequestBase<"dialog.chooseFolder", undefined>

export type ResponseChooseFolder = JSONRPCResponseBase<{
    path: string;
}>

export type RequestGetXtermConfig = JSONRPCRequestBase<"xterm.getConfig", {
    variant?: "light" | "dark";
}>;

export type RequestConfigGet = JSONRPCRequestBase<"config.get", undefined>;

export type RequestConfigSet = JSONRPCRequestBase<"config.set", {
    config: Record<string, unknown>;
}>;

type JSONRPCResponseBase<T extends Record<string, any> = Record<string, any>> = {
    jsonrpc: "2.0";
    id: string;
    result: T;
} | {
    jsonrpc: "2.0";
    id: string;
    error: {
        code: number;
        message: string;
        data?: Record<string, any>;
    }
}

export type JSONRPCResponse = {
    jsonrpc: "2.0";
    id: string;
    result?: Record<string, any>;
    error?: {
        code: number;
        message: string;
        data?: Record<string, any>;
    };
}

/** Hosts released before the protocol check answer `{}`. */
export type ResponseInitialize = JSONRPCResponseBase<{
    version?: string;
    protocol?: number;
}>

export type ResponseGetXtermConfig = JSONRPCResponseBase

export type ResponseCreateTTY = JSONRPCResponseBase<{
    id: string;
    url: string;
    /** The host's id for the session's pane tab. */
    pane?: string;
}>

export type ResponseAttachTTY = ResponseCreateTTY

/** One live session in tty.list's answer. */
export type ListedTTY = {
    id: string;
    /** What the session's program last reported (Durable only). */
    status?: unknown;
    title?: string;
    /** The host's id for its pane tab. */
    pane?: string;
    /** The agent-definition id it runs, absent for a shell. */
    agent?: string;
    /** What approval prompts call it: the agent's name or Terminal. */
    label?: string;
    /** The Chrome window it was last shown in (windowName), absent once that window closed. */
    window?: string;
    /** Set when it runs in the Durable (preview) backend and outlives the side panel. */
    durable?: boolean;
};

export type ResponseListTTY = JSONRPCResponseBase<{
    sessions: ListedTTY[];
}>

export type AgentInfo = {
    id: string;
    name: string;
    command: string;
    enabled: boolean;
    available: boolean;
    source: "shipped" | "user";
    icon: string;
};

export type ResponseConfigGet = JSONRPCResponseBase<{
    config: Record<string, unknown>;
    themes: string[];
    agents: AgentInfo[];
    path: string;
}>

export type ResponseConfigSet = JSONRPCResponseBase<{
    config: Record<string, unknown>;
}>;