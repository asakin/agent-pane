# Contributing to Agent Pane

Bug reports, documentation improvements, and focused pull requests are welcome. For larger changes, open an issue first so we can agree on the intended behavior and scope. Open contributions in this repository.

Use [GitHub Issues](https://github.com/asakin/agent-pane/issues) for bugs and feature proposals. Include your OS, Chrome version, Agent Pane version or commit, steps to reproduce, and what you expected. Remove credentials, private page content, and terminal secrets from logs. Report security problems privately as described in [SECURITY.md](SECURITY.md).

To develop locally, follow [Run a checkout](README.md#run-a-checkout). The required Go version is in `go.mod`; CI uses Node 26. Run the [checks in the README](README.md#checks) before submitting a pull request. If you change `install.sh`, also run `./install_test.sh`.

Keep each pull request focused, explain the behavior it changes, and describe what you tested. Add regression coverage for behavioral changes. See [AGENTS.md](AGENTS.md) for development instructions and checks.

Use the [shared terminology](README.md#terminology) in issues, pull requests, and documentation: distinguish pane tabs, Chrome tabs, coding agents, and PTY sessions.

Contributions are provided under the repository's [MIT License](LICENSE). Preserve existing copyright notices and attribution. Everyone participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
