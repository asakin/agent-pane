# Changelog

Notable changes to Agent Pane, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Entries start with v0.4.0; earlier releases are on the [releases page](https://github.com/asakin/agent-pane/releases).

## [Unreleased]

### Added

- A **Settings** button (⚙) at the right of the side panel's tab strip holds the everyday settings, each saved at once: a **Theme** picker of five popular dark terminal color themes (Tomorrow Night, the default, Dracula, Solarized Dark, Nord and Gruvbox Dark), which recolors the pane tab straight away, and **Keep the agent menu alphabetical**. **All settings…** opens Options, which keeps every setting. Both edit the same `config.json` values.
- Durable pane tabs keep their arrangement when Chrome quits, crashes or is killed: each Chrome window's pane tabs, their order, names, the active one and their locks are kept in a layout document in Agent Pane's private core ([RFC 0004](docs/rfcs/0004-pane-tab-layout-document.md)). After Chrome starts again, the **N running** list in every window offers each window's pane tabs as a group, and **Open all** opens them at once, in order, with the same one active. Approvals do not carry over, and a lock does only while the same Chrome run continues.
- Pane tabs can be reordered by dragging them along the side panel's tab strip, which marks where the tab will land, or with Ctrl+Shift+Left and Ctrl+Shift+Right on a focused tab. The active pane tab stays active, the order survives closing and reopening the side panel, and Durable (preview) pane tabs keep it in the layout document after Chrome quits.
- Rename a pane tab by double-clicking it or pressing F2 on it: Enter saves, Escape cancels. The name replaces the terminal's title in the tab strip, its tooltip and the **N running** list, and an empty name brings the title back. It survives closing and reopening the side panel, and a Durable (preview) pane tab keeps it when Chrome quits, as its name in the layout document.

### Changed

- Durable is the default terminal backend and no longer a preview: new pane tabs run in the bundled core and keep running when Chrome quits, the extension reloads or Agent Pane updates. Built-in stays available under **Terminal backend** in Options, and a `config.json` that already sets `"terminalBackend": "builtin"` keeps it.
- Options lists every bundled terminal color theme under **Theme**, and a new **Theme in dark mode** picker sets `themeDark`, which before could only be set in `config.json`.
- When the side panel can't reach the Agent Pane program, it explains that the program runs the terminals, shows the install command for this platform (`install.ps1` on Windows, `install.sh` elsewhere) with a Copy button, and links to the latest release. The update screen shows the Windows command on Windows too.

### Fixed

- The theme picked in Options now colors pane tabs when Chrome is in dark mode; before, they always used Tomorrow Night there. Saving a theme also drops the `"themeDark": "Tomorrow Night"` that earlier versions wrote into every new `config.json`; any other `themeDark` set there still takes its place in dark mode.
- On macOS, `agent-pane page` reads from a terminal no longer end now and then as soon as they start, with "timed out waiting for Agent Pane" or "denied: nobody answered the approval prompt in the side panel" before the person could answer.

## [0.4.2] - 2026-11-21

### Added

- Browser control setup: `agent-pane setup browser` and a **Browser control** section in Options report whether Node, npm and the Playwriter CLI are installed and show the setup steps for Playwriter and, optionally, Playwright MCP. The agent skill says when to read with `agent-pane page` and when to act with a browser-control tool. Agent Pane installs neither tool.
- `agent-pane --skill` prints the installed Agent Pane skill, and the side panel's **Teach agent** button, beside Send tab, asks the agent in the selected pane tab to run it and follow what it prints.

### Fixed

- Copy and paste in a pane tab follow the platform's terminals: Cmd+C and Cmd+V on macOS; Ctrl+C (with a selection), Ctrl+V and Ctrl+Shift+C/V on Windows; Ctrl+Shift+C/V on Linux. Ctrl+Shift+C no longer opens the browser's element inspector, and Ctrl+V pastes on Windows instead of sending ^V.

## [0.4.1] - 2026-11-20

### Fixed

- The macOS binaries are signed and notarized again. In v0.4.0 the signing step found no darwin build and skipped them.

### Added

- A privacy policy, [PRIVACY.md](PRIVACY.md).

## [0.4.0] - 2026-11-20

### Added

- Windows x86_64 support for Chrome, Edge, Chromium and Brave, installed with `install.ps1`. Page reads are not available on Windows yet; Send tab works.
- Durable (preview) terminal backend, off by default: pane tabs run in a private, bundled copy of herdr-core and keep running when Chrome quits, the extension reloads or Agent Pane updates.
- Agent status marks in the tab strip for Durable pane tabs, reported with `agent-pane status`.
- Helper agents in Durable pane tabs: `agent-pane run`, `send`, `wait`, `read` and `ls`.
- `agent-pane doctor` checks the bundled herdr-core against its pinned checksums and licenses.

### Changed

- The bundled herdr-core starts outside the browser where it can, so pane tabs do not inherit the browser's process properties, such as macOS's quarantine mark: on macOS through the LaunchAgent, and on Linux in a systemd user scope when the user's systemd is reachable. Without the LaunchAgent or a reachable systemd, it starts from the browser as before.

### Known issues

- The macOS binaries in this release are not signed or notarized.

[Unreleased]: https://github.com/asakin/agent-pane/compare/v0.4.2...HEAD
[0.4.2]: https://github.com/asakin/agent-pane/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/asakin/agent-pane/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/asakin/agent-pane/compare/v0.3.0...v0.4.0
