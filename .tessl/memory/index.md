# Memory Index

Use this file to keep durable project memory short and navigable.
When a topic deserves its own memory file, reference it here with a markdown link.

## Active Memory Files

- None yet.

## Project Notes

- Tessl verifiers live in `verifiers/` and are configured from the `verify` block in `tessl.json`.
- Do not add a CI job for Tessl verification; the Tessl Reviewer GitHub App runs verifiers on every pull request.
- Useful verifier checks before PRs: `tessl change verify lint verifiers/*.json`, a dry-run over all files, and a small `--sample` run.
- Herdr core migration context is documented in `docs/herdr-core-integration.md`, with an `AGENTS.md` pointer. Before changing terminal/session architecture, PTY host behavior, tab lifecycle, agent launching, or terminal attachment, read that doc. The intended direction is to keep Agent Pane’s own side-panel UI while using Herdr core / `hc` behind the scenes in the server/host layer.
- For substantial in-repo design proposals, use a lightweight Rust-style RFC process: create a top-level `rfcs/` directory with `README.md`, and numbered RFC files such as `rfcs/0001-herdr-core-backend.md`. Preferred RFC sections: Summary, Motivation, Guide-level explanation, Reference-level explanation, Drawbacks, Rationale and alternatives, Prior art, Unresolved questions, Future possibilities.
- The repo contains a Tessl skill at `skills/agent-pane/SKILL.md`. To evaluate it, run Tessl skill/plugin commands against `skills/agent-pane`, not the repo root: `tessl skill import skills/agent-pane`, `tessl scenario generate skills/agent-pane --count 5 --wait`, and `tessl eval run skills/agent-pane --wait`.
- Do not run `tessl plugin new` at the repo root unless intentionally creating a new Tessl plugin. Plain `tessl scenario generate` at the repo root expects a plugin directory with `.tessl-plugin/plugin.json` and is not the right command for this app repo.
