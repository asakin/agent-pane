# Contributing to Agent Pane

Bug reports, documentation improvements, and focused pull requests are welcome. For larger changes, open an issue first so we can agree on the intended behavior and scope. Open contributions in this repository.

Substantial product or architecture proposals may also start as an RFC following [`rfcs/README.md`](rfcs/README.md). RFCs are useful for changes to core architecture, protocols, trust boundaries, installer/distribution behavior, or product concepts that other work will build on. They are not required for ordinary issues or pull requests.

Use [GitHub Issues](https://github.com/asakin/agent-pane/issues) for bugs and feature proposals. Include your OS, Chrome version, Agent Pane version or commit, steps to reproduce, and what you expected. Remove credentials, private page content, and terminal secrets from logs. Report security problems privately as described in [SECURITY.md](SECURITY.md).

To develop locally, follow [Build from source](README.md#build-from-source). The required Go version is in `go.mod`; CI uses Node 26. Run the [checks in the README](README.md#checks) before submitting a pull request.

Keep each pull request focused, explain the behavior it changes, and describe what you tested. Add regression coverage for behavioral changes. See [AGENTS.md](AGENTS.md) for development instructions and checks.

Use the [shared terminology](README.md#terminology) in issues, pull requests, and documentation: distinguish pane tabs, Chrome tabs, coding agents, and PTY sessions.

Contributions are provided under the repository's [MIT License](LICENSE). Preserve existing copyright notices and attribution. Everyone participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Releasing

Releases are made from tags. Pushing a `vX.Y.Z` tag runs [`.github/workflows/release.yml`](.github/workflows/release.yml), which builds the extension and the program, publishes the GitHub release with an `install.sh` that installs that tag, and then installs the published release on Linux and macOS and runs `agent-pane doctor` on it.

1. If the extension now needs something older programs lack, raise `protocolVersion` in `internal/cmd/serve.go` and `MIN_HOST_PROTOCOL` in `extension/entrypoints/shared/hostVersion.ts`, and set `MIN_HOST_VERSION` to the version being released.
2. Tag `main` and push the tag: `git tag vX.Y.Z && git push origin vX.Y.Z`. X, Y and Z are numbers only: the version becomes the extension's manifest version, which Chrome requires to be numeric, so a suffix such as `-rc.1` does not make a loadable release.
