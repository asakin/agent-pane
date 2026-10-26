#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export AGENT_TERMINAL_SKIP_LAUNCHD=1

bin="$tmp/fake-agent-terminal"
printf '#!/bin/sh\necho agent-terminal\n' >"$bin"
chmod 755 "$bin"

home="$tmp/home"
mkdir -p "$home/Library/Application Support/Google/Chrome"
HOME="$home" SHELL=/bin/zsh AGENT_TERMINAL_BIN="$bin" "$root/install.sh" >/dev/null

manifest="$home/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.agentterminal.native.json"
host="$home/.local/share/agent-terminal/native_messaging_host"
[ -s "$manifest" ]
grep -F 'chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/' "$manifest" >/dev/null
grep -F "$host" "$manifest" >/dev/null
[ -x "$host" ]
grep -F "$home/.local/bin/agent-terminal" "$host" >/dev/null
[ -x "$home/.local/bin/agent-terminal" ]
grep -q '# agent-terminal:path' "$home/.zshrc"
grep -c 'agent-terminal:start' "$home/AGENTS.md" | grep -qx 1
grep -c 'agent-terminal:start' "$home/CLAUDE.md" | grep -qx 1

HOME="$home" SHELL=/bin/zsh AGENT_TERMINAL_BIN="$bin" "$root/install.sh" >/dev/null
grep -c 'agent-terminal:path' "$home/.zshrc" | grep -qx 1
grep -c 'agent-terminal:start' "$home/AGENTS.md" | grep -qx 1

printf 'hello\n<!-- agent-terminal:start -->\nold\n<!-- agent-terminal:end -->\nbye\n' >"$home/AGENTS.md"
HOME="$home" SHELL=/bin/zsh AGENT_TERMINAL_BIN="$bin" "$root/install.sh" >/dev/null
grep -qx 'hello' "$home/AGENTS.md"
grep -qx 'bye' "$home/AGENTS.md"
grep -c 'agent-terminal:start' "$home/AGENTS.md" | grep -qx 1
grep -q 'Playwright' "$home/AGENTS.md"

if [ "$(uname -s)" = Darwin ]; then
	plist="$home/Library/LaunchAgents/com.agentterminal.spawn.plist"
	[ -s "$plist" ]
	grep -F "$home/.local/bin/agent-terminal" "$plist" >/dev/null
	grep -F '<string>spawn</string>' "$plist" >/dev/null
fi

empty="$tmp/empty"
mkdir -p "$empty"
if HOME="$empty" SHELL=/bin/zsh AGENT_TERMINAL_BIN="$bin" "$root/install.sh" >/dev/null 2>&1; then
	echo "install succeeded with no browser directory" >&2
	exit 1
fi

echo "install_test: ok"
