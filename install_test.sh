#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export AGENT_PANE_SKIP_LAUNCHD=1

bin="$tmp/fake-agent-pane"
printf '#!/bin/sh\necho agent-pane\n' >"$bin"
chmod 755 "$bin"

home="$tmp/home"
if [ "$(uname -s)" = Darwin ]; then
	chrome="$home/Library/Application Support/Google/Chrome"
	chrome_hosts="$chrome/NativeMessagingHosts"
else
	chrome="$home/.config/google-chrome"
	chrome_hosts="$chrome/native-messaging-hosts"
fi
mkdir -p "$chrome"
HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null

manifest="$chrome_hosts/com.agentpane.native.json"
host="$home/.local/share/agent-pane/native_messaging_host"
[ -s "$manifest" ]
grep -F 'chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/' "$manifest" >/dev/null
grep -F "$host" "$manifest" >/dev/null
if grep -F 'allowed_extensions' "$manifest" >/dev/null; then
	echo "manifest names a non-Chromium extension" >&2
	exit 1
fi
[ -x "$host" ]
grep -F "$home/.local/bin/agent-pane" "$host" >/dev/null
[ -x "$home/.local/bin/agent-pane" ]
grep -q '# agent-pane:path' "$home/.zshrc"
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1
grep -c 'agent-pane:start' "$home/CLAUDE.md" | grep -qx 1

HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null
grep -c 'agent-pane:path' "$home/.zshrc" | grep -qx 1
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1

printf 'hello\n<!-- agent-pane:start -->\nold\n<!-- agent-pane:end -->\nbye\n' >"$home/AGENTS.md"
HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null
grep -qx 'hello' "$home/AGENTS.md"
grep -qx 'bye' "$home/AGENTS.md"
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1
grep -q 'Playwright' "$home/AGENTS.md"
grep -q 'agent-pane page' "$home/AGENTS.md"
grep -q 'untrusted' "$home/AGENTS.md"
[ -f "$home/.local/share/agent-pane/agents/enabled.json" ]
[ -f "$home/.local/share/agent-pane/agents/claude/agent.json" ]
[ -f "$home/.local/share/agent-pane/agents/goose/icon.png" ]
[ ! -e "$home/.config/agent-pane/agents" ]
ls -ld "$home/.local/share/agent-pane" | grep -q '^drwx------'

grep -q 'AGENT_PANE_SKILL_PATH' "$home/AGENTS.md"

# The skill: one canonical copy, and an owned copy per skills folder.
skill="$home/.local/share/agent-pane/skills/agent-pane/SKILL.md"
cmp "$root/skills/agent-pane/SKILL.md" "$skill"
for dir in "$home/.agents/skills/agent-pane" "$home/.claude/skills/agent-pane"; do
	cmp "$root/skills/agent-pane/SKILL.md" "$dir/SKILL.md"
	[ -f "$dir/.agent-pane-managed" ]
done
grep -qx 'name: agent-pane' "$skill"
grep -q '^description: ' "$skill"

# Updating replaces owned copies; folders that are not ours stay as they are.
echo stale >"$home/.agents/skills/agent-pane/SKILL.md"
echo extra >"$home/.claude/skills/agent-pane/old.md"
mkdir -p "$home/.agents/skills/mine"
echo mine >"$home/.agents/skills/mine/SKILL.md"
HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null
cmp "$root/skills/agent-pane/SKILL.md" "$home/.agents/skills/agent-pane/SKILL.md"
[ ! -e "$home/.claude/skills/agent-pane/old.md" ]
grep -qx mine "$home/.agents/skills/mine/SKILL.md"

# A run cut short leaves its staging folder; the next run still installs.
rm -rf "$home/.claude/skills/agent-pane"
mkdir -p "$home/.local/share/agent-pane/skills/.agent-pane-copy"
echo partial >"$home/.local/share/agent-pane/skills/.agent-pane-copy/SKILL.md"
HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null
cmp "$root/skills/agent-pane/SKILL.md" "$home/.claude/skills/agent-pane/SKILL.md"
[ -f "$home/.claude/skills/agent-pane/.agent-pane-managed" ]
[ ! -e "$home/.local/share/agent-pane/skills/.agent-pane-copy" ]

rm -rf "$home/.claude/skills/agent-pane"
mkdir -p "$home/.claude/skills/agent-pane"
echo user >"$home/.claude/skills/agent-pane/SKILL.md"
rm -rf "$home/.agents/skills/agent-pane"
ln -s "$home/.agents/skills/mine" "$home/.agents/skills/agent-pane"
HOME="$home" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null 2>&1
grep -qx user "$home/.claude/skills/agent-pane/SKILL.md"
[ -L "$home/.agents/skills/agent-pane" ]
grep -qx mine "$home/.agents/skills/mine/SKILL.md"

if [ "$(uname -s)" = Darwin ]; then
	plist="$home/Library/LaunchAgents/com.agentpane.spawn.plist"
	[ -s "$plist" ]
	grep -F "$home/.local/bin/agent-pane" "$plist" >/dev/null
	grep -F '<string>spawn</string>' "$plist" >/dev/null
fi

empty="$tmp/empty"
mkdir -p "$empty"
if HOME="$empty" SHELL=/bin/zsh AGENT_PANE_BIN="$bin" "$root/install.sh" >/dev/null 2>&1; then
	echo "install succeeded with no browser directory" >&2
	exit 1
fi

echo "install_test: ok"
