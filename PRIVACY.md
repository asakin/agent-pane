# Privacy policy

This policy covers the Agent Pane Chrome extension and the `agent-pane` program it works with. Both are published by Ariel Sakin at https://github.com/asakin/agent-pane.

## What Agent Pane collects

Nothing. Agent Pane has no server, no account, no analytics and no telemetry. The extension sends nothing to external services: it talks only to the `agent-pane` program on the same computer, through Chrome's native messaging and a WebSocket on the local loopback address (`127.0.0.1`) that carries the terminal.

## What Agent Pane handles on your computer

- **Page content.** When a program running in a pane tab asks to read the Chrome tab beside it, and you allow it, the extension reads that tab (its URL, title, selected text, text, Markdown or page structure, depending on the request). **Send tab** does the same when you click it. The content goes only to the `agent-pane` program on the same computer, and from there only to the terminal that asked. Domains you block in Options are never read.
- **Tab details.** The extension reads the URL and title of tabs to show which tab a pane tab is reading and to apply your blocked domains.
- **Settings and state.** Your options (theme, font size, shell, enabled agents, blocked domains and similar) are kept in `~/.config/agent-pane/` on your computer. Chrome's extension storage keeps a random browser ID that tells your Chrome windows apart, your recently used agents, and which pane tabs belong to which window.

None of this is sent to the publisher or to any third party by Agent Pane.

## Programs you run

Agent Pane starts the coding agents and shells you choose, such as Claude Code, Codex or Pi. Those programs run as your user, and what they do with page content you let them read, including sending it to their own services, is governed by their own terms and privacy policies, not this one.

## Changes

Changes to this policy are made in this file, and its history is in the repository.

## Contact

Open an issue at https://github.com/asakin/agent-pane/issues, or report security issues privately as described in [SECURITY.md](SECURITY.md).
