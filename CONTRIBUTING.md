# Contributing to Agent Pane

Bug reports, documentation improvements, and focused pull requests are welcome. For larger changes, open an issue first so we can agree on the intended behavior and scope. Open contributions in this repository.

Substantial product or architecture proposals may also start as an RFC following [`docs/rfcs/`](docs/rfcs). RFCs are useful for changes to core architecture, protocols, trust boundaries, installer/distribution behavior, or product concepts that other work will build on. They are not required for ordinary issues or pull requests.

Use [GitHub Issues](https://github.com/asakin/agent-pane/issues) for bugs and feature proposals. Include your OS, Chrome version, Agent Pane version or commit, steps to reproduce, and what you expected. Remove credentials, private page content, and terminal secrets from logs. Report security problems privately as described in [SECURITY.md](SECURITY.md).

To develop locally, follow [Build from source](README.md#build-from-source). The required Go version is in `go.mod`. Run the [development checks](AGENTS.md#commands) before submitting a pull request.

Keep each pull request focused, explain the behavior it changes, and describe what you tested. Add regression coverage for behavioral changes.

Use the [shared terminology](README.md#terminology) in issues, pull requests, and documentation: distinguish pane tabs, Chrome tabs, coding agents, and PTY sessions.

Contributions are provided under the repository's [MIT License](LICENSE). Preserve existing copyright notices and attribution. Everyone participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
