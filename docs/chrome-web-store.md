# Chrome Web Store listing

The text for the Chrome Web Store listing and its review forms, kept here so each submission uses the same answers. The privacy policy is [PRIVACY.md](../PRIVACY.md).

## Store listing

**Name:** Agent Pane

**Summary** (the manifest `description`, 132 characters at most): Claude Code, Codex and Pi in a real terminal, in Chrome's side panel.

**Category:** Developer Tools

**Description:**

> Agent Pane puts a real terminal in Chrome's side panel and starts the coding agents you already use in it: Claude Code, Codex, Pi, OpenCode, Goose, or any other command on your PATH. Your agents, your tools and your files, running on your own computer, beside the page you are working on.
>
> Agent Pane needs its companion program, installed with one command:
>
> macOS and Linux:
> curl -fsSL https://github.com/asakin/agent-pane/releases/latest/download/install.sh | sh
>
> Windows (PowerShell):
> irm https://github.com/asakin/agent-pane/releases/latest/download/install.ps1 | iex
>
> What it does:
> • Pane tabs: several terminals in the side panel, each running a shell or a coding agent.
> • One-click agents: start Claude Code, Codex, Pi and others from the launcher.
> • The page beside the agent: an agent can read the tab next to it (URL, selection, text, Markdown or page structure), only after you allow it, per pane tab. Domains you block are never read.
> • Send tab: paste the current page into a pane tab as Markdown.
> • Durable pane tabs: agents keep running when you close Chrome.
>
> Privacy: Agent Pane has no server, account, analytics or telemetry. Page content goes only to the terminal on your computer that asked for it.
>
> Agents run commands on your computer with your user account. Agent Pane is not a sandbox. Read the Security section of the README before using it.
>
> Open source (MIT): https://github.com/asakin/agent-pane

**Homepage URL:** https://github.com/asakin/agent-pane

**Support URL:** https://github.com/asakin/agent-pane/issues

**Privacy policy URL:** https://github.com/asakin/agent-pane/blob/main/PRIVACY.md

## Privacy practices

**Single purpose:** Run the user's own terminal and coding agents in Chrome's side panel, and let those agents read the current page when the user allows it.

**Permission justifications:**

| Permission | Justification |
| --- | --- |
| `nativeMessaging` | The terminals run in the Agent Pane program on the user's computer. The extension starts and talks to it through native messaging; without it the extension cannot open a terminal. |
| `sidePanel` | The terminals are shown in Chrome's side panel, which is the extension's whole interface. |
| `tabs` | To show which tab a pane tab reads, follow the active tab of the window, apply the user's blocked domains to a tab's URL, and keep each window's pane tabs with that window. |
| `scripting` | To read the current page when the user clicks Send tab or approves an agent's page-read request. The script runs only in response to those actions; there are no content scripts. |
| `storage` | To keep a random browser ID, recently used agents, and which pane tabs belong to which window. |
| Host permission `<all_urls>` | The page an agent is asked to read can be on any site. A read happens only after the user's approval or Send tab click, never in the background, and blocked domains are never read. |

**Remote code:** No. All code is in the package; the extension does not load or run code from anywhere else.

**Data usage:**

- Collected: **Website content**, only when the user approves a page read or clicks Send tab. It is passed to the Agent Pane program on the same computer and is not sent to the publisher or anyone else. It is whatever the page shows, so a page the user chooses to share can contain personal communications, financial details or other personal information (a webmail or bank page, for example); domains in the user's blocked list are never read.
- Not gathered separately: Agent Pane does not collect personally identifiable information, health, financial, authentication, personal communications, location, web history or user activity outside the website content above.
- Certifications: the data is not sold or transferred to third parties, is not used or transferred for purposes unrelated to the single purpose, and is not used or transferred to determine creditworthiness or for lending.

## Notes for reviewers

> Agent Pane needs its companion program, which the extension talks to over native messaging. Without it, the side panel shows a setup screen with the install command.
>
> To test on macOS or Linux:
> 1. Run: curl -fsSL https://github.com/asakin/agent-pane/releases/latest/download/install.sh | sh
> 2. Install the extension, then click the Agent Pane toolbar icon to open the side panel.
> 3. A pane tab opens with a shell. Start an agent from the agent menu in the tab strip, or type commands.
> 4. Page reading: in a pane tab, run `agent-pane page info`. The side panel asks for approval; after Allow, the command prints the current tab's URL and title.
> 5. Send tab: click the Send tab button; the page is pasted into the pane tab as Markdown.
>
> The program is open source: https://github.com/asakin/agent-pane. The extension sends nothing to external services; it talks only to the local program, over native messaging and a loopback (127.0.0.1) WebSocket for the terminal.
