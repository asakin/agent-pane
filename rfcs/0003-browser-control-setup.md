# RFC 0003: Browser control setup

- **Status:** Proposed
- **Issue:** #74
- **Author:** Tessl, for @asakin

## Summary

Help a person set up a browser-control tool for their coding agents, so an agent in a pane tab can click, type and navigate in the person's own Chrome, without Agent Pane attaching to the browser itself. Agent Pane gains one implementation of the setup in the Go program, reached two ways: an `agent-pane setup browser` command an agent or a person can run, and a **Browser control** section in Options that shows what is installed and runs the same setup. The main tool is [Playwriter](https://github.com/remorses/playwriter) (MIT): an npm CLI any agent can run, plus its own Chrome extension. [Playwright MCP](https://github.com/microsoft/playwright-mcp) (Apache-2.0) with its "Playwright Extension" is an optional addition for agents that support MCP. Setup is opt-in. It finds Node and npm on the login shell's `PATH` and reports them missing rather than installing them, installs the Playwriter CLI with npm into the person's own npm prefix, registers Playwright MCP only through the agent's own `mcp add` command, and opens the Chrome Web Store page for each extension, since only the person can add an extension. The agent skill says when to read with `agent-pane page` and when to act with the browser-control tool. Options, the command and the README say plainly that controlling the browser with these tools is the person's choice and responsibility, and that page-read approvals and blocked domains apply to `agent-pane page` only. The extension's permissions do not change. The work is one pull request; [Rollout](#rollout) gives its commits.

## Motivation

Agent Pane reads pages and does not act on them. `skills/agent-pane/SKILL.md` says: "These commands only read. There is no command to navigate, click, type or fill forms." The instructions block the installer writes (`scripts/instructions.md`) says: "Browser automation is whatever CLI you already run, such as Playwright or Playwriter. This extension does not attach to a browser and does not choose one." So an agent asked to fill a form or click through a page beside it has nothing to do it with unless the person already installed and configured a browser-control tool, and nothing in Agent Pane helps them find one, install it or tell whether it works.

What the repository has today that the setup can build on:

- **Finding programs.** `loginPATH()` in `internal/cmd/agents.go` reads the `PATH` a login shell uses (`printenv PATH` through `-lc`), because the host's own `PATH` is Chrome's; on Windows it is the registry `Path` plus the browser's (`windowsPATH`). `findExecutable` and `executableNames` resolve a name with `PATHEXT` on Windows, so `npm.cmd` is found. The launcher already uses these to list agents.
- **Native-messaging RPCs.** Options talks to the host with `config.get` and `config.set` (`extension/entrypoints/shared/rpc.ts`, handled in `internal/cmd/serve.go`). Each handler runs in its own goroutine (`internal/jsonrpc/host.go`), and `nativeTimeoutMs` in `extension/entrypoints/shared/nativeRpc.ts` gives a request 5 seconds unless the method has its own limit, as `dialog.chooseFolder` has 120 seconds.
- **Options.** `extension/entrypoints/options.html` has fields for the shell, theme, font size, Terminal backend, environment, starting folder, agents and blocked domains. There is no section about other tools.
- **Extension permissions.** `extension/wxt.config.ts` asks for `nativeMessaging`, `scripting`, `sidePanel`, `storage` and `tabs`, and `<all_urls>`. Opening a Web Store page in a new tab with `chrome.tabs.create` needs nothing more. Detecting whether another extension is installed (`chrome.management`) would need the `management` permission, which `AGENTS.md` says requires maintainer approval.
- **CLI.** `NewCmdRoot` in `internal/cmd/root.go` registers `serve`, `spawn`, `page`, `status`, `ls`, `run`, `send`, `wait`, `read`, `doctor` and `place`. There is no `setup` command.

The outcome this RFC aims for: a person opens Options, sees under **Browser control** that Node is found and Playwriter is not, clicks **Set up**, adds the Playwriter extension from the Web Store page Agent Pane opens, clicks its icon on a Chrome tab, and then asks Claude Code in a pane tab to fill in the form on that tab; the agent reads the page with `agent-pane page` and fills the form with `playwriter`.

## Guide-level explanation

### For a person using Agent Pane

Options has a new **Browser control** section. It starts with what browser control means here, in these words or close to them:

> Agents can click, type and navigate in your Chrome with a browser-control tool. Agent Pane does not control the browser; these tools do, with their own Chrome extensions and their own warnings. Using them is your choice and your responsibility. Agent Pane's page-read approvals and blocked domains apply to `agent-pane page` only, not to these tools: an agent using one can act on any tab you connect to it.

Under that, a status list:

| Item | Shows |
| --- | --- |
| Node and npm | Found, with versions, or "not found" with a link to the Node.js download page |
| Playwriter CLI | Installed, with version, or not |
| Playwriter extension | A button that opens its Chrome Web Store page, and whether a Chrome tab is connected to Playwriter now, when that can be told (see [Extension status](#extension-status)) |
| Playwright MCP (optional) | For each agent on the launcher that has an `mcp add` command: registered or not |
| Playwright Extension (optional) | A button that opens its Chrome Web Store page |

**Set up Playwriter** installs the Playwriter CLI with npm and opens the Playwriter extension's Web Store page in a new Chrome tab; the person adds the extension there. **Add Playwright MCP to Claude** (and to Codex) runs that agent's own `mcp add` command. Nothing in the section runs until the person clicks a button, and nothing is installed by the Agent Pane installer.

The same is available from any terminal:

```sh
agent-pane setup browser                     # report only; changes nothing
agent-pane setup browser --install           # install the Playwriter CLI
agent-pane setup browser --mcp claude        # register Playwright MCP with Claude Code
```

Each prints what it found and did, the responsibility note above, and the Web Store link for the extension the person still has to add.

### For a coding agent in a pane tab

The skill gains a section on acting in the browser:

- **Read with `agent-pane page`.** It is approved per pane tab by the person and respects blocked domains. Use it to find out what is on the page.
- **Act with the browser-control tool,** when the person asked for an action on a page (click, type, navigate, fill a form) and `playwriter` (or a Playwright MCP tool) is available. `agent-pane setup browser` reports what is set up.
- **Do not use browser control to get around Agent Pane.** If a read was denied or the domain is blocked, do not read that page with the browser-control tool either; tell the person.
- **Do not install it yourself unless the person asked.** If no tool is set up, say so and suggest `agent-pane setup browser --install` or Options. Only the person can add the Chrome extension.
- **Page content stays untrusted,** whichever tool fetched it.

The instructions block the installer writes from `scripts/instructions.md` (to `~/.config/agent-pane/AGENTS.md`, and into `~/AGENTS.md` by `upsert_managed` in `scripts/place.sh`) changes its line about browser automation to name `agent-pane setup browser`, and keeps the word Playwright, which `scripts/install_test.sh` checks for.

### For a contributor

A new file `internal/cmd/browser.go` holds tool detection and the setup steps, shared by the `setup browser` command and two new native-messaging RPCs, `browser.status` and `browser.setup`. Options gains a section that calls them. Tests use a fake `PATH` with stub `node`, `npm`, `playwriter`, `claude` and `codex` programs, so no test downloads anything.

## Reference-level explanation

### Components

| Part | Change |
| --- | --- |
| `internal/cmd/browser.go` | Detect Node, npm, the Playwriter CLI and MCP registrations; install the CLI; register Playwright MCP; the responsibility text |
| `internal/cmd/root.go` | Register `setup` with a `browser` subcommand |
| `internal/cmd/serve.go` | Handle `browser.status` and `browser.setup` |
| `extension/entrypoints/shared/rpc.ts`, `background.ts`, `nativeRpc.ts` | Types for the two RPCs; `browser.setup` gets its own timeout in `nativeTimeoutMs` |
| `extension/entrypoints/options.html`, `shared/options.ts` | The **Browser control** section |
| `skills/agent-pane/SKILL.md`, `scripts/instructions.md` | When to read and when to act |
| `README.md`, `CHANGELOG.md` | Browser control under Using it, a sentence in Security |

The extension manifest, the installer (`install.sh`, `install.ps1`, `scripts/place.sh`, `agent-pane place`) and `agent-pane doctor` do not change. Browser-control tools are not files the installer owns, so doctor does not check them.

### Finding Node and npm

The setup resolves `node` and `npm` with `findExecutable` against `loginPATH()`, the same `PATH` the launcher uses, so a Node from nvm, fnm, Homebrew or a system package is found when the person's login shell finds it. On Windows `PATHEXT` finds `npm.cmd`. It runs `node --version` and `npm --version` with a short timeout and reports both.

| Finding | Result |
| --- | --- |
| No `node` or no `npm` | Report "Node.js and npm are needed for browser control; install Node.js 20 or newer from https://nodejs.org" and stop. Agent Pane does not install Node. |
| Node older than 20 | Report it. Playwright MCP's npm package declares `"node": ">=20"`; Playwriter's does not declare an engine, so the setup uses the same minimum for both. |
| Both found | Continue. |

npm is run with the login `PATH` in its environment, so `npm`'s own child processes find the same `node`.

### Installing the Playwriter CLI

`agent-pane setup browser --install` (and **Set up Playwriter**) runs:

```sh
npm install --global playwriter@<version>
```

- **Where it goes.** Into npm's global prefix for that Node: under the version manager's folder for nvm or fnm, under Homebrew's prefix on macOS, under `%APPDATA%\npm` on Windows. None of these need administrator rights. A Node from a Linux distribution package usually has a prefix under `/usr`, where a global install fails with `EACCES`. The setup never runs `sudo`; it reports npm's error and suggests setting a user prefix (`npm config set prefix ~/.local`) or using a version manager.
- **Version.** `<version>` is a version Agent Pane names in `browser.go` and updates in its releases, as it pins herdr-core in `scripts/herdr-core.lock`. Pinning means a release is tested against a known Playwriter; it also means a person gets Playwriter fixes only when Agent Pane moves the pin, unless they update it themselves. Whether to pin or install `latest` is an [unresolved question](#unresolved-questions).
- **Already installed.** If `playwriter` is on the login `PATH`, the setup reports its version and does not reinstall, unless `--install` is given again with `--force`.
- **After installing,** the setup checks that `playwriter` now resolves on the login `PATH`. When npm's global bin folder is not on it, the setup says which folder to add.
- **Playwriter's own skill.** Playwriter offers `npx -y skills add https://playwriter.dev -g -y`, which installs its agent skill into the agents' skill folders. The setup does not run it; the Agent Pane skill carries the guidance agents need and points at `playwriter --help`. See [Unresolved questions](#unresolved-questions).
- **Telemetry.** Playwriter's README says it sends anonymous usage statistics to Strada, without code, URLs or page content, and that `PLAYWRITER_TELEMETRY=0` or `DO_NOT_TRACK=1` turns it off. The setup's output and Options say so and how to turn it off. Agent Pane does not set either variable on the person's behalf.

### Registering Playwright MCP

Playwright MCP is optional and only offered for agents whose CLI has an `mcp add` command that the [Playwright MCP README](https://github.com/microsoft/playwright-mcp) documents. Of Agent Pane's shipped agent definitions (`agents/`), that is `claude` and `codex`:

| Agent | Command the setup runs |
| --- | --- |
| `claude` | `claude mcp add playwright -- npx @playwright/mcp@<version> --extension` |
| `codex` | `codex mcp add playwright -- npx @playwright/mcp@<version> --extension` |

These are the README's commands (`claude mcp add playwright npx @playwright/mcp@latest`, `codex mcp add playwright npx "@playwright/mcp@latest"`) with a pinned version and `--extension` added, after `--` so the agent's CLI passes the flag on rather than reading it.

- `--extension` makes Playwright MCP connect to the person's running Chrome through the Playwright Extension instead of starting its own browser. The extension asks the person, on first use, to approve the connection and choose which tabs or tab group the client may control.
- The setup runs the agent's own command and never edits an agent's configuration file. An agent's `mcp add` decides where the entry goes and in what scope; the setup reports what the command printed.
- `browser.status` reports whether `playwright` is registered by running `claude mcp get playwright` and `codex mcp get playwright` when those exist, and "unknown" otherwise. Which subcommands each agent supports for this is a [prototype question](#what-needs-a-prototype).
- For other agents (Pi, OpenCode, Goose, Cursor Agent), Options and the command print the JSON entry from the Playwright MCP README for the person to add themselves.
- Playwright MCP's extension token (`PLAYWRIGHT_MCP_EXTENSION_TOKEN`, shown by the extension, which saves approving every connection) is not handled by Agent Pane. The person copies it into their agent's MCP configuration if they want it. Agent Pane does not store or pass it.

`npx` fetches `@playwright/mcp` when the agent first starts the server, so nothing is installed at registration time beyond the agent's configuration entry.

### Extension status

Only the person can add a Chrome extension. The setup opens the Web Store page and does not try to install anything into the browser:

| Extension | Web Store page |
| --- | --- |
| Playwriter | https://chromewebstore.google.com/detail/playwriter/jfeammnjpkecdekppnclgkkffahnhfhe |
| Playwright Extension | https://chromewebstore.google.com/detail/playwright-extension/mmlmfjhmonkocbjadbfplnigmagldckm |

Options opens the page with `chrome.tabs.create`. The command prints the link. Neither needs a new permission.

Telling whether an extension is installed is harder. `chrome.management` would answer it but needs the `management` permission, which this RFC does not ask for. For Playwriter, its README says the extension connects to a local relay on `localhost:19988` when the person clicks its icon on a tab. The setup can ask the Playwriter CLI whether a browser is connected (`playwriter session list` or the relay's status), which tells "connected now", not "installed". Options shows that as "Connected" or "Not connected: add the extension and click its icon on a Chrome tab". Whether the CLI has a stable way to ask this is a [prototype question](#what-needs-a-prototype). For the Playwright Extension, Options shows only the Web Store button.

### Native-messaging RPCs

Added as `AGENTS.md` describes: types in `rpc.ts`, forwarded through `sendNative` in `background.ts`, handled in `serve.go`, covered in `extension/tests/nativeRpc.test.mjs` and `internal/cmd/agents_test.go`.

| Method | Params | Result |
| --- | --- | --- |
| `browser.status` | none | `{ node, npm, playwriter, playwriterConnected, mcp: [{ agent, registered }] }`, each tool as `{ found, version, path }` or `"unknown"` |
| `browser.setup` | `{ action: "install-playwriter" }` or `{ action: "add-mcp", agent }` | `{ ok, output }`, where `output` is the text the command form prints |

`browser.setup` runs npm or the agent CLI, which can take a minute or more. Its handler already runs in its own goroutine, so it does not block other requests. `nativeTimeoutMs` gives it 10 minutes, and the host gives the child process the same limit and kills it after. Options disables the buttons while a setup runs and shows the output when it returns. Neither RPC changes the `protocolVersion`, since an older host answers them with "method not found" and Options then hides the section with "Update Agent Pane to set up browser control here".

The RPCs and the command share one function per action, so the Options path and the terminal path cannot drift.

### The skill

`skills/agent-pane/SKILL.md` gains a section after "Read the page", and its sentence "These commands only read. There is no command to navigate, click, type or fill forms." gains a pointer to it. The section says what [the guide](#for-a-coding-agent-in-a-pane-tab) says, and names the commands: `agent-pane setup browser` to see what is set up, `playwriter --help` for Playwriter, the agent's own MCP tools for Playwright MCP. A new eval scenario under `skills/agent-pane/evals/` covers an agent asked to fill a form on a page whose read was denied: the agent should not read or fill it with Playwriter.

### Trust and responsibility

These tools act outside every boundary Agent Pane has:

- **Approvals and blocked domains** live in the host and the extension and apply to reads through the page socket or descriptor. Playwriter and the Playwright Extension use Chrome's `debugger` permission and `<all_urls>` host access (their manifests in `remorses/playwriter` `extension/manifest.json` and `microsoft/playwright` `packages/extension/manifest.json`); Agent Pane cannot see or stop what they do.
- **Which tabs.** Playwriter's README says it controls only tabs the person clicked its icon on, plus tabs its sessions open, and that Chrome shows an automation banner on them. The Playwright Extension asks the person to choose tabs or a tab group on first connection. These are the tools' own controls, and Agent Pane's text names them without vouching for them.
- **Their own warnings.** Playwright MCP's README says it "is **not** a security boundary". Playwriter's README says its remote control feature "is not a sandbox". Options links to both READMEs.
- **Same-user programs.** Playwriter's relay listens on `localhost:19988`. Agent Pane's README already says any program running as the person can read Chrome's profile and run `agent-pane`; a running relay adds a way for such a program to drive connected tabs. The README's Security section says so.
- **Opt-in.** Nothing is installed by the Agent Pane installer or on first run. Setup runs only from a button in Options or the command with `--install` or `--mcp`. The skill tells agents not to run it unless the person asked.

Agent Pane does not pass its blocked domains to these tools. Playwright MCP has `--blocked-origins`, but its README says it "does not serve as a security boundary and does not affect redirects", so passing the list would suggest a protection that does not exist.

### Platforms

| Platform | Node and npm | Global install | Notes |
| --- | --- | --- | --- |
| macOS | Login shell `PATH` (`zsh -lc`) finds Homebrew, nvm, fnm | Homebrew and version-manager prefixes need no `sudo` | |
| Linux | Login shell `PATH` | Distribution Node fails with `EACCES`; the setup reports it | |
| Windows | Registry `Path` plus the browser's (`windowsPATH`), `PATHEXT` finds `npm.cmd`, `npx.cmd` | `%APPDATA%\npm`, no administrator rights | The CLI runs `npm.cmd` through `cmd.exe`, as the launcher does for `.cmd` agents. Playwriter's README lists "Windows WSL" as supported and does not mention native Windows, so native Windows is a [prototype question](#what-needs-a-prototype). |

The extensions are Chrome Web Store items. Brave and Vivaldi install from the Chrome Web Store; Edge does after the person turns on "Allow extensions from other stores". The Playwright Extension's README names Chrome, Edge and Chromium.

### Licenses and distribution

| Component | License | How Agent Pane uses it |
| --- | --- | --- |
| Playwriter CLI (`playwriter` on npm, `remorses/playwriter`) | MIT (repository `LICENSE`; the npm package has no `license` field) | npm installs it into the person's npm prefix on request |
| Playwriter extension | MIT (same repository) | Agent Pane links to its Web Store page |
| Playwright MCP (`@playwright/mcp`) | Apache-2.0 | The agent's `mcp add` records an `npx` command; npm fetches it when the agent starts it |
| Playwright Extension (`microsoft/playwright`, `packages/extension`) | Apache-2.0 (repository license) | Agent Pane links to its Web Store page |

Agent Pane does not put any of these in its release archive, its extension or its repository. It runs the person's own npm and links to Web Store pages, so it redistributes nothing and has no notices to ship. The README names the tools and their licenses where it describes the setup.

### Chrome Web Store

Agent Pane is not on the Chrome Web Store yet (the README's Install section loads it unpacked). For the listing when it exists:

- **Permissions.** No change, so no new permission warning and no new justification.
- **Remote code.** The Web Store's [Manifest V3 requirements](https://developer.chrome.com/docs/webstore/program-policies/mv3-requirements) forbid an extension from running code it did not ship in its package. The extension still downloads and runs nothing; npm runs in the native host on the person's request, like every program in a pane tab. The listing's description of the native host says it can install a browser-control CLI with npm when asked.
- **Single purpose.** The Web Store's [quality guidelines](https://developer.chrome.com/docs/webstore/program-policies/quality-guidelines) require a narrow, easily understood purpose. Browser control setup serves the same purpose, coding agents working beside a page, and adds no unrelated feature. Opening another extension's Web Store page is a link, not an install, and is allowed.
- **Linking to other extensions.** Links to the Playwriter and Playwright Extension pages send the person to those items' own listings and permission prompts. The listing should not suggest Agent Pane endorses or reviews them.

### Testing

- **Go.** `browser_test.go` with a temporary directory of stub programs on `PATH`: Node missing, Node too old, npm failing with `EACCES`, Playwriter already present, a successful install, the `mcp add` command line for `claude` and `codex`, an agent with no `mcp add`, and the timeout. Windows stubs as `.cmd` files run on the `go-windows` job. The `browser.*` handlers in `agents_test.go`.
- **Extension.** `nativeRpc.test.mjs` for the two methods and the `browser.setup` timeout; a test that Options hides the section when the host answers "method not found".
- **Skill.** The new eval scenario.
- **Manual, recorded in the pull request:** on macOS, Linux and Windows, set up Playwriter from Options with Node from a version manager, add the extension, connect a tab, and have Claude Code fill a form on it; register Playwright MCP with Claude Code and use it with the Playwright Extension; a Linux distribution Node reports the `EACCES` advice.

### What needs a prototype

1. How to tell, from the CLI, whether a Chrome tab is connected to Playwriter, and whether that is stable enough to show in Options.
2. Whether `claude mcp get` and `codex mcp get` (or `mcp list`) report a registration in a form the setup can read, and whether both CLIs accept `--` before the server command.
3. Playwriter's CLI and relay on native Windows, not WSL.
4. `npm install --global` through the native host on each platform: the environment npm needs beyond `PATH` (`HOME`, `APPDATA`), and npm's behavior with no terminal.

## Drawbacks

- **Agent Pane becomes the place people ask about tools it does not maintain.** A Playwriter or Playwright MCP bug, or a changed install command, will look like an Agent Pane bug.
- **A softer trust story.** Agent Pane's page reads are approved per pane tab; making it easy to install a tool that acts on any connected tab without those approvals may blur that for people who do not read the note.
- **Pinned versions go stale,** and an unpinned install changes under a release without testing.
- **Running npm from the host** adds a long-running, network-using action to a program that until now only ran terminals and read pages.
- **Partial status.** Without the `management` permission, Options cannot say whether an extension is installed, only, for Playwriter, whether a tab is connected now.

## Rationale and alternatives

**Recommended: one Go implementation of an opt-in setup, reached from Options and from a command, that installs only the CLI and registers MCP only through the agent's own command.** It keeps Agent Pane out of the browser, asks for no permission, writes nothing outside npm's own prefix and the agent's own configuration, and makes the command an agent can run the same as the button a person clicks.

Alternatives considered:

| Alternative | Why not |
| --- | --- |
| **Do nothing** (keep the one line in `scripts/instructions.md`) | No new code or support load. The cost: every person who wants an agent to act on the page has to find a tool, install it, find its extension and configure their agent alone, and agents have no guidance on when to act versus read, so some will try to use a browser-control tool to read pages a person denied. |
| **Documentation only** (a README section and skill text, no command or Options section) | Cheap and gets most of the guidance, including the skill's read-or-act rule. It leaves Node detection, the install and the status to the person. A reasonable smaller first step if review prefers it. |
| **Agent Pane attaches to Chrome itself** (`debugger` permission, its own `agent-pane click` commands) | A new trust boundary and a large permission in Agent Pane's own extension, which `AGENTS.md` says needs maintainer approval, and a reimplementation of what Playwriter and Playwright already do. Not proposed. |
| **The installer installs Playwriter** | Makes browser control the default, against the issue's requirement that setup is opt-in, and adds npm as an installer dependency. |
| **Bundle Playwriter in the release archive** | Ships and must keep up with someone else's Node program and its dependencies; Agent Pane would also have to bundle Node. |
| **Playwright MCP as the main option** | Works only in agents that support MCP. Playwriter's CLI works in any agent with a shell tool, which is every agent Agent Pane launches. |
| **Edit agents' MCP configuration files directly** | Each agent has its own file, format and scopes, and they change. The agents' own `mcp add` commands are their supported interface. |
| **Ask for `management` to detect extensions** | Better status, but a new permission and warning for a status line. |
| **Run Playwriter's `npx skills add`** | Installs a second, third-party skill into the same folders Agent Pane manages copies in (`~/.claude/skills`, `~/.agents/skills`); left to the person. |

## Prior art

- **Agent Pane's herdr-core pin** (`scripts/herdr-core.lock`, RFC 0001): a third-party program pinned per release. This RFC proposes the same for Playwriter, without bundling it.
- **The launcher's agent detection** (`loginPATH`, `findExecutable` in `internal/cmd/agents.go`): the same lookup the setup uses for Node, npm and agent CLIs.
- **Playwright MCP's README** lists install commands per client (`claude mcp add`, `codex mcp add`, `code --add-mcp`) rather than editing files, which is the approach proposed for registration.
- **Playwriter's `@playwriter/install-mcp`** (its `MCP.md`) auto-configures MCP clients; Agent Pane does not use it, preferring the agents' own commands.
- **Playwriter's `playwriter browser start`** starts a separate Chrome with the extension bundled; it avoids the Web Store step but does not drive the person's own Chrome, which is what this issue asks for.

## Unresolved questions

- **Pin or latest.** Install `playwriter@<pinned>` and `@playwright/mcp@<pinned>` (proposed), or `@latest`, as both projects' READMEs show?
- **Documentation-only first.** Should the first change be the skill, instructions and README text only, with the command and Options section after?
- **Telemetry.** Should the setup offer a checkbox that sets `PLAYWRITER_TELEMETRY=0` in Agent Pane's environment settings for pane tabs, or only tell the person how?
- **Playwriter's skill.** Should the setup offer to run `npx -y skills add https://playwriter.dev -g -y`, or should the Agent Pane skill stay the only one?
- **Install consent for agents.** Is the skill's "only if the person asked" enough, or should `--install` refuse unless Options has a "let agents set up browser control" setting turned on?
- **Native Windows.** If Playwriter does not work on native Windows, should the section say so and offer only Playwright MCP there?

## Future possibilities

- **Status in the side panel:** a mark when a browser-control tool is connected to the Chrome tab beside a pane tab.
- **More tools,** such as other CDP-based CLIs, behind the same section, if people ask for them.
- **Agent definitions with MCP servers:** a field in `agent.json` that lists MCP servers the launcher passes on start, for agents that accept them as arguments.
- **An Agent Pane action layer** with per-pane-tab approvals for clicks and typing, which would need its own RFC, since it is a new trust boundary and needs the `debugger` permission.

## Rollout

One implementation: one branch and one pull request that delivers browser control setup working end to end from Options and from the command. The pieces below are an ordered plan inside that pull request, one commit each.

1. **Detection and setup in Go.** `internal/cmd/browser.go`: Node, npm, Playwriter and MCP detection on the login `PATH`; the Playwriter install; `mcp add` for `claude` and `codex`; the responsibility and telemetry text; `browser_test.go` with stub programs, including Windows `.cmd` stubs.
2. **`agent-pane setup browser`.** The `setup` command with its `browser` subcommand, its flags and output, registered in `root.go`.
3. **Native-messaging RPCs.** `browser.status` and `browser.setup` in `rpc.ts`, `background.ts`, `nativeRpc.ts` (timeout) and `serve.go`, with `nativeRpc.test.mjs` and `agents_test.go` cases.
4. **Options section.** **Browser control** in `options.html` and `options.ts`: the note, the status list, the buttons, Web Store links through `chrome.tabs.create`, and the hidden state for an older host.
5. **Skill and instructions.** The skill section, the changed line in `scripts/instructions.md` (still naming Playwright, for `install_test.sh`), and the eval scenario.
6. **Documentation.** README: browser control under Using it, the relay and the tools' own boundaries under Security, the tools and their licenses. A CHANGELOG line under Added.

The pull request merges once a person can go from Options, or from `agent-pane setup browser`, to an agent in a pane tab acting on a connected Chrome tab with Playwriter on macOS and Linux, Playwright MCP registers with Claude Code and Codex, the manual checks in [Testing](#testing) are recorded, and both check suites pass. No part needs to ship separately: the change adds no permission and no new endpoint in Agent Pane.
