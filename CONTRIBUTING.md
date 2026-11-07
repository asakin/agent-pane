# Contributing to Agent Pane

Bug reports, documentation improvements, and focused pull requests are welcome. For larger changes, open an issue first so we can agree on the intended behavior and scope. Agent Pane has its own direction; please open contributions here rather than in its predecessor projects.

Use [GitHub Issues](https://github.com/asakin/agent-pane/issues) for bugs and feature proposals. Include your OS, Chrome version, Agent Pane version or commit, steps to reproduce, and what you expected. Remove credentials, private page content, and terminal secrets from logs. Report security problems privately as described in [SECURITY.md](SECURITY.md).

To develop locally, follow [Run a checkout](README.md#run-a-checkout). The required Go version is in `go.mod`; CI uses Node 26. Run the [checks in the README](README.md#checks) before submitting a pull request. If you change `install.sh`, also run `./install_test.sh`.

Keep each pull request focused, explain the behavior it changes, and describe what you tested. Add regression coverage for behavioral changes. Follow the architecture and security constraints in [AGENTS.md](AGENTS.md); do not hand-edit generated themes.

Contributions are provided under the repository's [MIT License](LICENSE). Preserve existing copyright notices and attribution. Everyone participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
