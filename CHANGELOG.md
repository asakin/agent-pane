# Changelog

Notable changes to Agent Pane, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Entries start with v0.4.0; earlier releases are on the [releases page](https://github.com/asakin/agent-pane/releases).

## [Unreleased]

### Added

- Durable (preview) pane tabs keep their arrangement when Chrome quits, crashes or is killed: each Chrome window's pane tabs, their order, names, the active one and their locks are kept in a layout document in Agent Pane's private core ([RFC 0004](docs/rfcs/0004-pane-tab-layout-document.md)). After Chrome starts again, the **N running** list in every window offers each window's pane tabs as a group, and **Open all** opens them at once, in order, with the same one active. Approvals do not carry over, and a lock does only while the same Chrome run continues.

### Changed

- When the side panel can't reach the Agent Pane program, it explains that the program runs the terminals, shows the install command for this platform (`install.ps1` on Windows, `install.sh` elsewhere) with a Copy button, and links to the latest release. The update screen shows the Windows command on Windows too.

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
