# Herdr-core integration context

Agent Pane may use Herdr core behind its own Chrome side-panel UI. Read this when changing terminal/session architecture, the PTY host, tab lifecycle, agent launching, or terminal attachment.

## Status

The Durable (preview) terminal backend (`internal/cmd/backend_core.go`, Options **Terminal backend**, config key `terminalBackend: "durable"`) is RFC 0001's smallest safe slice: `tty.create` runs a core terminal labelled with the session's public ids, the WebSocket replay frame is the core's first paint, input and `tty.resize` go over the attach stream, `tty.destroy` kills and removes the terminal, and the page socket admits the core-reported pid's descendants. There is no fd-3 page channel. Sessions end with the host as Built-in ones do. When the core cannot start a pane tab, it runs in Built-in with a notice; when the host cannot tell whether `terminal.run` started it, it fails `tty.create` instead of falling back, and tries to kill and remove whatever carries the session's label. Until the core confirms that cleanup, the host keeps retrying it, no other Durable pane tab starts (each fails `tty.create`), and host shutdown tries it once more; if the core stays unreachable, that program may keep running in the core. A broken event subscription is resubscribed rather than treated as the program ending. The same subscription carries the program's status (OSC 7501) and title: the host keeps the latest per session, sends each change to the extension as a `tty.event` notification (`{tty, status, title}`), and returns them in `tty.list`, and the side panel shows the status as a mark in the tab strip. `agent-pane status <state> [msg]` writes the report to the process's terminal for programs that cannot print it themselves. Built-in stays the default.

## Goal

The migration goal is to keep Agent Pane as the product surface while giving it Herdr-core power:

- The Chrome side panel remains Agent Pane's UI.
- The extension keeps using xterm.js unless a change explicitly replaces the renderer.
- The native host keeps mediating Chrome native messaging, page-read approvals, install/doctor behavior, and extension security boundaries.
- Herdr core provides durable terminal and agent process management behind that UI.
- Each Agent Pane pane tab can map to a Herdr-core terminal/pane identity, so agents can discover, message, start, and wait on each other through Agent Pane affordances.
- Users should not need to know Herdr is present; this remains Agent Pane, installed and operated as Agent Pane.

Do not migrate the project to the Herdr app's layouts, mods, gpui-kit UI, or generated `~/.config/herdr-app` files unless an issue explicitly asks for that. Agent Pane can take product inspiration from the Herdr app, but the relevant backend is herdr-core, controlled by the `hc` CLI/API.

## Current Agent Pane terminal model

Today Agent Pane owns its terminal backend:

- `extension/entrypoints/shared/rpc.ts` defines native-messaging RPCs such as `tty.create`, `tty.attach`, `tty.resize`, `tty.list`, and `tty.destroy`.
- `extension/entrypoints/shared/terminal.ts`, `windowTabs.ts`, and `ptyAttach.ts` create side-panel tabs, attach xterm.js to a WebSocket, resize sessions, and reattach after reconnects.
- `internal/cmd/serve.go` handles native-messaging RPCs, starts local PTYs, serves the PTY WebSocket, tracks sessions, issues one-time attach tokens, and replays scrollback.
- `internal/cmd/pane.go` keeps pane-tab identity and page-read grants separate from the current PTY process.
- `internal/cmd/env.go` and page-channel code provide Agent Pane environment variables and page-read access to programs running in the terminal.

A Herdr-core migration should preserve the extension-facing contract first, then replace internals behind it. Avoid changing Chrome permissions or extension origins unless the issue explicitly requires it.

## What Herdr core is

Herdr has two separate parts:

- `herdr-app`: the desktop window with layouts, panels, keys, and mods.
- `herdr-core`, exposed by the `hc` command: the terminal and agent process manager.

For Agent Pane, `hc` is the part to integrate. It can start terminals, keep them alive, list them, read their screen/history, send input, wait for agents, label terminals, run commands on remote machines, and expose an API for app builders.

Use `hc --help` and per-command help as the source of truth. The local generated Herdr app context at `~/.config/herdr-app/AGENTS.md` is not available in Tessl cloud runs and should not be imported wholesale.

## Design stance

The proposed migration shape is directionally right but not trivial:

- The extension can mostly keep its current shape because Agent Pane already has xterm.js, native messaging, and a Go host that owns terminal sessions.
- The Go host should become a bridge to Herdr core instead of being removed. It still owns Agent Pane-specific trust, page access, install checks, and the extension-facing protocol.
- Herdr core can replace most local PTY process ownership, durable terminal state, keep/recovery behavior, terminal discovery, agent status, and cross-agent control.
- Some current host behavior may remain as adapter logic: WebSocket authorization, resize coalescing, stream bridging, page-channel environment, and compatibility with existing extension RPCs.
- The migration should be designed as Agent Pane embedding a private core backend, not as requiring a user-installed Herdr app or a user-visible `hc` install.

Be careful with broad deletion claims. Herdr core may replace the PTY implementation, scrollback storage, and process persistence, but Agent Pane still needs reconnect semantics at the extension boundary and tests proving replay/input behavior is safe.

## Useful `hc` surfaces

Core commands agents should know about:

- `hc run [--bg] [--keep] [--cwd <dir>] [--name <name>] [--label k=v] -- <cmd...>` starts a program in a managed terminal. With no command it starts the configured shell.
- `hc ls --json` lists live terminals; `--all` includes finished terminals; `--label k=v` filters.
- `hc get <terminal> --json` returns one terminal's facts.
- `hc read <terminal>` prints screen and history as text.
- `hc attach <terminal>` attaches an interactive terminal.
- `hc send <terminal> <text>` pastes text and presses Enter.
- `hc keys <terminal> <keys...>` sends named keys such as `enter`, `esc`, `up`, or `ctrl+c`.
- `hc wait <terminal>` waits until the program stops working; use this instead of sleeps in automation.
- `hc interrupt <terminal>` sends the appropriate stop key and reports what happened.
- `hc kill <terminal>` ends a program.
- `hc keep <terminal> [--off]` controls recovery of lost programs as shells.
- `hc label <terminal> k=v...` sets labels used for discovery and grouping.
- `hc events --json` streams terminal fact changes.
- `hc connect` speaks the core protocol over stdin/stdout.
- `hc schema` prints the API as an OpenRPC document; use it instead of guessing request/response shapes.

Herdr-core settings live under `~/.config/herdr-core/config.toml`. Do not rely on Herdr app files for core behavior.

## API methods to inspect first

Run `hc schema` before designing or coding against the core API. Methods of interest include:

- `hello`: handshake, protocol generation, core identity, methods, and framed connection mode.
- `server.status`, `server.stop`, `server.restart`: core status and lifecycle.
- `terminal.run`, `terminal.list`, `terminal.get`: terminal creation and discovery.
- `terminal.attach`: raw stream attachment to a live terminal on a framed connection.
- `terminal.read`: screen and history as text.
- `terminal.send`, `terminal.keys`, `terminal.interrupt`, `terminal.wait`: input and agent-control operations.
- `terminal.kill`, `terminal.rm`, `terminal.keep`, `terminal.label`: lifecycle and metadata.
- `events.subscribe`: initial snapshot and ordered fact changes.
- `machine.*`: remote-machine support.
- `file.*`, `machine.exec`, `doc.*`: auxiliary remote file, command, and document operations.
- `rpc.discover`: runtime API discovery.

Prefer runtime discovery over hard-coded assumptions where feasible. If the host pins to a minimum `hc` version or protocol generation, make the check explicit and test the failure path.

## Integration shape

Prefer this layering unless an issue chooses another design:

1. Keep extension RPC names stable where possible: `tty.create`, `tty.attach`, `tty.resize`, `tty.list`, and `tty.destroy` remain the side panel's contract.
2. Add a host-side adapter that maps those RPCs to Herdr-core terminals.
3. Keep Agent Pane pane-tab identity and page-read grant ownership in the Agent Pane host, not in Herdr core.
4. Use Herdr labels to associate core terminals with Agent Pane state, for example an Agent Pane pane id, tab id, window id, or app id.
5. Use `hc connect`/core API for long-lived integration; use shelling out to `hc` only for probes, prototypes, or simple one-shot operations.
6. Keep xterm.js rendering in the side panel by attaching to a core terminal stream, rather than embedding the Herdr app UI.
7. Preserve existing security boundaries: Chrome origin checks, one-time attach tokens, native-messaging trust, and page-read approval prompts.
8. Treat every visible feature as Agent Pane functionality. `hc` and Herdr-core concepts can power implementation, but user-facing names, settings, and docs should stay Agent Pane-native unless deliberately exposed.

A small first step can run Herdr core alongside the current PTY backend behind a feature flag. A later step can make Herdr core the default backend and remove duplicated PTY/session code once behavior and tests converge.

## Bundling Herdr core

Agent Pane should not require a separate Herdr installation.

Design constraints for shipping core privately:

- Bundle a pinned `hc`/herdr-core binary inside Agent Pane's own release folder, not in `~/.local/bin`, so it does not overwrite or depend on a user's Herdr install.
- Run the bundled core in a separate session, such as `HC_SESSION=agent-pane`, so sockets, state, and terminals stay separate from any Herdr app the user also runs.
- Pin the supported core version and protocol generation. Upgrade it only through Agent Pane releases unless the project explicitly adds another update mechanism.
- Include Herdr-core license and notices. Agent Pane is MIT; Herdr core is Apache-2.0, and release packaging should include the output or content required by `hc --licenses`.
- Put the bundled `hc` on `PATH` inside Agent Pane terminals so agents can use the Agent Pane-scoped core to start and message sub-agents.
- Keep Chrome Web Store messaging clear that Agent Pane runs a native host and terminals may keep running after Chrome closes.
- Check platform support before making core mandatory. If Herdr core lacks a target Agent Pane supports, design a fallback, a platform exclusion, or a staged rollout.

The installer, doctor, release archives, and update path must all understand the private core binary and its session state.

## Behavior to preserve or intentionally redesign

Account for these Agent Pane behaviors when replacing the backend:

- Pane tabs are logical UI tabs. A running process can be replaced while the pane tab's page-read grants survive.
- Closing a pane tab revokes its page-read grants.
- Each Agent Pane agent tab should correspond to a core terminal/pane record that can be rediscovered after Chrome, the extension, or the host restarts.
- The extension attaches through a host-issued, one-time WebSocket URL today. If `terminal.attach` replaces that path, provide an equivalent authorization boundary between the extension and terminal stream.
- Resize events are coalesced before they reach the backend.
- The host currently replays scrollback before accepting input, to prevent replayed terminal queries from sending responses to the shell. If core attachment has different replay semantics, prove the input-safety property another way.
- Agent launchers come from `agents/<id>/agent.json` plus user overrides. If Herdr templates or program tables are introduced, define how they coexist with Agent Pane's agent catalog.
- Agent Pane's page channel and `agent-pane page` commands are project features. Keep their environment variables and socket lifecycle working for commands launched through Herdr core.
- Agents should be able to use the Agent Pane CLI and bundled `hc` to start helper agents, message other pane agents, wait for them, and discover status without exposing Herdr as the product.
- Tests cover native RPC shape, session lifecycle, page grants, peer credentials, install checks, and terminal input. Update or replace those tests with the backend change.

## Labels and grouping

Use labels instead of terminal names for Agent Pane bookkeeping. Names are user-visible and must stay optional; labels are better for stable grouping and filtering.

Candidate labels:

- `agent-pane.pane=<pane id>` for the Agent Pane logical pane tab.
- `agent-pane.window=<browser/window id>` when needed for side-panel state.
- `agent-pane.app=<agent id>` when launched from an Agent Pane agent definition.
- `agent-pane.cwd=<normalized cwd>` only if needed and safe; avoid leaking unnecessary local paths into durable labels.

Herdr app uses labels such as `herdr.space=<id>` and `herdr.tab=<id>` for its own UI. Do not use those unless intentionally interoperating with the Herdr app.

## Product opportunities

Herdr core enables Agent Pane features beyond a backend swap:

- **Durable panes:** terminals and agents can survive extension reloads, host restarts, and Agent Pane upgrades.
- **Agent status:** pane tabs can show `working`, `blocked`, `done`, and `idle` where agents report status.
- **Agent teamwork:** agents can start helper agents, pass files or messages, wait for results, and surface blocked helpers to the user.
- **Remote machines:** the same side panel can manage terminals on configured remote machines if Agent Pane chooses to expose that.
- **Session recovery:** pane tabs can rediscover existing core terminals by label instead of treating host reconnects as fresh sessions.
- **Richer automation:** Agent Pane CLI commands can route through the core for wait/read/send workflows instead of scraping terminals directly.

These are design inputs, not automatic migration requirements.

## Development checks

When working on the integration:

- Run `hc --help`, the relevant `hc <command> --help`, and `hc schema` in the development environment before coding against `hc` behavior.
- Keep generated or machine-local Herdr files out of the repository.
- Do not require the Herdr app to be running for Agent Pane tests or normal side-panel use.
- Do not require a user-installed `hc`; tests and release behavior should use Agent Pane's configured or bundled core.
- Add tests at the adapter boundary so the extension-facing RPCs can be validated without a real long-lived core where possible.
- Run the standard Go and extension checks from `AGENTS.md` before pushing.
