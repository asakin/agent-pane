# Changelog

Notable changes to Agent Pane, newest first. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Entries start with v0.4.0; earlier releases are on the [releases page](https://github.com/asakin/agent-pane/releases).

## [Unreleased]

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

- On macOS and Linux the bundled herdr-core starts outside the browser (through the LaunchAgent, or a systemd user scope), so pane tabs no longer inherit the browser's process properties, such as macOS's quarantine mark.

### Known issues

- The macOS binaries in this release are not signed or notarized.

[Unreleased]: https://github.com/asakin/agent-pane/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/asakin/agent-pane/compare/v0.3.0...v0.4.0
