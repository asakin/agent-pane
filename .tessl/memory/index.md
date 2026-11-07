# Memory Index

Use this file to keep durable project memory short and navigable.
When a topic deserves its own memory file, reference it here with a markdown link.

## Active Memory Files

- None yet.

## Project Notes

- Tessl verifiers live in `verifiers/` and are configured from the `verify` block in `tessl.json`.
- Do not add a CI job for Tessl verification; the Tessl Reviewer GitHub App runs verifiers on every pull request.
- Useful verifier checks before PRs: `tessl change verify lint verifiers/*.json`, a dry-run over all files, and a small `--sample` run.
