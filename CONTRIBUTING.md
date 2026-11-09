# Contributing to Agent Pane

Bug reports, documentation improvements, and focused pull requests are welcome. For larger changes, open an issue first so we can agree on the intended behavior and scope. Open contributions in this repository.

Substantial product or architecture proposals may also start as an RFC following [`rfcs/README.md`](rfcs/README.md). RFCs are useful for changes to core architecture, protocols, trust boundaries, installer/distribution behavior, or product concepts that other work will build on. They are not required for ordinary issues or pull requests.

Use [GitHub Issues](https://github.com/asakin/agent-pane/issues) for bugs and feature proposals. Include your OS, Chrome version, Agent Pane version or commit, steps to reproduce, and what you expected. Remove credentials, private page content, and terminal secrets from logs. Report security problems privately as described in [SECURITY.md](SECURITY.md).

To develop locally, follow [Build from source](README.md#build-from-source). The required Go version is in `go.mod`; CI uses Node 26. Run the [checks in AGENTS.md](AGENTS.md#commands) before submitting a pull request.

Keep each pull request focused, explain the behavior it changes, and describe what you tested. Add regression coverage for behavioral changes. See [AGENTS.md](AGENTS.md) for development instructions and checks.

Use the [shared terminology](README.md#terminology) in issues, pull requests, and documentation: distinguish pane tabs, Chrome tabs, coding agents, and PTY sessions.

Contributions are provided under the repository's [MIT License](LICENSE). Preserve existing copyright notices and attribution. Everyone participating is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Releasing

Releases are made from tags. Pushing a `vX.Y.Z` tag runs [`.github/workflows/release.yml`](.github/workflows/release.yml), which builds the extension and the program, signs and notarizes the macOS binaries, publishes the GitHub release with an `install.sh` that installs that tag, and then installs the published release on Linux and macOS, runs `agent-pane doctor` on it, and on macOS checks that the binary passes `codesign --verify --strict` and that Gatekeeper accepts it as notarized Developer ID software.

Each archive carries the herdr-core build that [`scripts/herdr-core.lock`](scripts/herdr-core.lock) pins for its platform: `scripts/fetch-herdr-core.sh` downloads it, checks the archive and binary checksums, and lays out `libexec/hc`, `herdr-core.lock` and `licenses/herdr-core/` (herdr-core's `LICENSE` and `THIRD-PARTY-NOTICES`, the text `hc --licenses` prints). To move to another herdr-core release, update the lock's version, URLs and checksums from `https://releases.herdr.dev/core/<version>/manifest.json` and the binaries in those archives.

### One-time signing setup

GoReleaser signs and notarizes the darwin binaries with its built-in `notarize.macos` step (`.goreleaser.yaml`), which runs on the Linux release runner and needs no Mac. A maintainer sets this up once:

1. Join the [Apple Developer Program](https://developer.apple.com/programs/) as an organization or individual. Only the Account Holder can create a Developer ID certificate.
2. Create a **Developer ID Application** certificate (Xcode > Settings > Accounts > Manage Certificates, or the Certificates page of the developer account), then export it with its private key from Keychain Access as a `.p12` file protected by a password.
3. In [App Store Connect > Users and Access > Integrations > Team Keys](https://appstoreconnect.apple.com/access/integrations/api), create an API key with the Developer role. Download the `.p8` file (it can be downloaded only once) and note the key ID and the issuer ID shown above the key list.
4. Add these repository secrets under Settings > Secrets and variables > Actions:

   | Secret | Value |
   | --- | --- |
   | `MACOS_SIGN_P12` | the `.p12` file, base64-encoded: `base64 -i cert.p12 \| tr -d '\n'` |
   | `MACOS_SIGN_PASSWORD` | the `.p12` password |
   | `MACOS_NOTARY_KEY` | the `.p8` file, base64-encoded the same way |
   | `MACOS_NOTARY_KEY_ID` | the API key ID |
   | `MACOS_NOTARY_ISSUER_ID` | the issuer ID |

The release workflow in `asakin/agent-pane` fails before building if any of these is missing. In a fork with none of them set, the release publishes unsigned darwin binaries with a warning and skips the signing check; a fork with only some of them set fails. The Developer ID certificate is valid for five years; renew it and update `MACOS_SIGN_P12` and `MACOS_SIGN_PASSWORD` before it expires.

### Making a release

1. If the extension now needs something older programs lack, raise `protocolVersion` in `internal/cmd/serve.go` and `MIN_HOST_PROTOCOL` in `extension/entrypoints/shared/hostVersion.ts`, and set `MIN_HOST_VERSION` to the version being released.
2. Tag `main` and push the tag: `git tag vX.Y.Z && git push origin vX.Y.Z`. X, Y and Z are numbers only: the version becomes the extension's manifest version, which Chrome requires to be numeric, so a suffix such as `-rc.1` does not make a loadable release.

## Skill evals

[`.github/workflows/skill-evals.yml`](.github/workflows/skill-evals.yml) runs [Tessl](https://tessl.io) evals for the [agent skill](README.md#agent-skill): the scenarios in `skills/agent-pane/evals/`, with `skills/agent-pane` as the context under test. It is advisory: it runs only when a maintainer starts it, never on a push or pull request, and is not a required check. To run it, open **Actions** → **skill-evals** → **Run workflow**, pick the branch, and optionally change the run label (default `agent-pane skill eval`). The job waits for the run and fails if the eval run fails; open the run in Tessl for the scores. It needs a Tessl API token for the `siglify/agent-pane` project stored as the `TESSL_TOKEN` repository secret. To run the same evals locally:

```sh
tessl eval run skills/agent-pane --label "agent-pane skill eval"
```
