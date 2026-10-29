<p align="center">
  <img src="media/agent-terminal-logo.png" width="128" alt="Agent Terminal logo">
</p>

<h1 align="center">Agent Terminal</h1>
<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <a href="https://developer.chrome.com/docs/extensions/mv3/"><img src="https://img.shields.io/badge/Chrome-MV3-4285F4?logo=googlechrome&logoColor=white" alt="Chrome"></a>
</p>

<p align="center">
A local shell in Chrome’s side panel.
</p>

Each terminal is a local shell. An agent in that shell uses whatever browser tools it already has.

**Note:** this project is experimental. Agents run shell commands on your machine. Use with care.

## Why Agent Terminal

The side panel is a terminal next to the browser, in the same window.

|                             | Agent Terminal                                   | IDE / Terminal.app alone                   |
| --------------------------- | ------------------------------------------------ | ------------------------------------------ |
| Where the terminal sits     | Chrome side panel, beside the page               | A separate app                             |
| You stay in the loop        | Same window                                      | You switch away to watch a terminal        |
| One-click agents            | The agent commands your shell can already run    | Manual shells and PATH                     |

## Install

Chrome, then:

```sh
curl -fsSL https://raw.githubusercontent.com/asakin/agent-terminal/main/install.sh | sh
```

The script downloads the binary for this machine, puts `~/.local/bin` on `PATH`, and writes the native-messaging manifest Chrome needs. On macOS it also starts a LaunchAgent that launches the shell, so programs the agent runs are not children of Chrome. If that manifest is not written, the script exits with an error. Running it again writes the same files again.

Then load the extension in Chrome:

1. Open `chrome://extensions`
2. Turn on **Developer mode**
3. Click **Load unpacked** and choose `~/.local/share/agent-terminal/extensions/chrome` (the installer copies it from the release), or `extension/dist/chrome-mv3` from a checkout
4. Open the side panel from the Agent Terminal extension icon

From a checkout, build the binary and point the same script at it:

```sh
git clone https://github.com/asakin/agent-terminal.git
cd agent-terminal
go build -o agent-terminal .
AGENT_TERMINAL_BIN="$PWD/agent-terminal" ./install.sh
cd extension && npm install && npm run build
```

## How it works

It is a terminal in Chrome’s side panel: your shell, local files, `git`, language toolchains, existing CLI/agent tools, and whatever else is on your `PATH`. The extension does not attach to a browser and does not choose one. Browser automation is whatever CLI the agent runs.

## Usage

- **Tabs:** up to 8 terminals per Chrome window
- **One-Click Agent Launcher:** the side panel opens a shell. `+` runs the last agent you chose. The menu lists enabled agents that are on your `PATH`, then Terminal. Opening one moves it to the top unless Settings is set to keep the menu alphabetical
- **Settings:** shell, theme, font size, env, which agents are on, and the folder new agent tabs start in. The shell tab still starts in your home directory. Definitions live in `agents/` (see `agents/README.md`)

Terminal editing follows VS Code/Cursor conventions. Shift+Enter inserts a newline in
supporting agent CLIs; on macOS, Cmd+Enter does too. Cmd+Backspace deletes to the start
of the line, Cmd+Left/Right moves to its start/end, and Option+Left/Right moves by word.
Option+Backspace sends Ctrl+W (delete the previous whitespace-delimited word), and
Option+Delete deletes forward by word. On Windows/Linux, Ctrl+Backspace/Delete deletes
backward/forward by word. Option/Alt+Up/Down sends Ctrl+Up/Down.

On macOS, Cmd+A selects the terminal buffer, Cmd+K clears the visible terminal history,
and Escape clears a selection. Page Up/Down, Cmd+Home/End and Cmd+Option+Page Up/Down
scroll by page, to the top/bottom and by line respectively. Scrolling shortcuts apply
only to the normal buffer, leaving alternate-screen applications in control of page
navigation. Copy/paste uses the browser's normal shortcuts. Browser-reserved shortcuts
and Cursor's editor, pane, search and shell-integration commands are not remapped.

## Features

- **Session survival:** hide the side panel and open it again; the same tabs are still running

## Contributing

PRs welcome. For large changes, open an issue first. Keep the extension free of page host permissions.

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE). Based on [Tweety](https://github.com/pomdtr/tweety) by Achille Lacoin.
