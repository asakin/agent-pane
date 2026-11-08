#!/bin/sh
# Tests install.sh, scripts/place.sh and `agent-pane doctor` against a release
# built from this checkout, in temporary HOMEs. Needs Go. Touches nothing in
# the real HOME and downloads nothing.
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export AGENT_PANE_SKIP_LAUNCHD=1
export SHELL=/bin/zsh

# A release folder laid out like the archive, with a stand-in extension.
rel="$tmp/release-src"
mkdir -p "$rel/extensions/chrome" "$rel/skills"
(cd "$root" && go build -o "$rel/agent-pane" .)
cp "$root/scripts/place.sh" "$rel/place.sh"
cp "$root/scripts/instructions.md" "$rel/instructions.md"
cp -R "$root/agents" "$rel/agents"
cp -R "$root/skills/agent-pane" "$rel/skills/agent-pane"
printf '{"manifest_version":3,"name":"Agent Pane","version":"0.0.0"}\n' >"$rel/extensions/chrome/manifest.json"

home="$tmp/home"
if [ "$(uname -s)" = Darwin ]; then
	chrome="$home/Library/Application Support/Google/Chrome"
	chrome_hosts="$chrome/NativeMessagingHosts"
else
	chrome="$home/.config/google-chrome"
	chrome_hosts="$chrome/native-messaging-hosts"
fi
mkdir -p "$chrome"

place() {
	HOME="$home" sh "${1:-$rel/place.sh}"
}
doctor() {
	HOME="$home" "$home/.local/bin/agent-pane" doctor "$@"
}

place >"$tmp/first.out"

data="$home/.local/share/agent-pane"
manifest="$chrome_hosts/com.agentpane.native.json"
host="$data/native_messaging_host"
[ -s "$manifest" ]
grep -F 'chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/' "$manifest" >/dev/null
grep -F "$host" "$manifest" >/dev/null
if grep -F 'allowed_extensions' "$manifest" >/dev/null; then
	echo "manifest names a non-Chromium extension" >&2
	exit 1
fi
[ -x "$host" ]
grep -F "$home/.local/bin/agent-pane" "$host" >/dev/null
cmp "$rel/agent-pane" "$home/.local/bin/agent-pane"
cmp "$rel/place.sh" "$data/release/place.sh"
cmp "$rel/extensions/chrome/manifest.json" "$data/extensions/chrome/manifest.json"
grep -q '# agent-pane:path' "$home/.zshrc"
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1
grep -c 'agent-pane:start' "$home/CLAUDE.md" | grep -qx 1
grep -q '^ok    release' "$tmp/first.out"
ls -ld "$data" | grep -q '^drwx------'

# A second run changes nothing.
place >"$tmp/second.out"
if grep '^agent-pane: updated' "$tmp/second.out"; then
	echo "second run changed something" >&2
	exit 1
fi
grep -c 'agent-pane:path' "$home/.zshrc" | grep -qx 1
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1

# Running the installed copy of place.sh works the same.
place "$data/release/place.sh" >"$tmp/third.out"
if grep '^agent-pane: updated' "$tmp/third.out"; then
	echo "the installed place.sh changed something" >&2
	exit 1
fi

printf 'hello\n<!-- agent-pane:start -->\nold\n<!-- agent-pane:end -->\nbye\n' >"$home/AGENTS.md"
place >/dev/null
grep -qx 'hello' "$home/AGENTS.md"
grep -qx 'bye' "$home/AGENTS.md"
grep -c 'agent-pane:start' "$home/AGENTS.md" | grep -qx 1
grep -q 'Playwright' "$home/AGENTS.md"
grep -q 'agent-pane page' "$home/AGENTS.md"
grep -q 'untrusted' "$home/AGENTS.md"
grep -q 'AGENT_PANE_SKILL_PATH' "$home/AGENTS.md"
[ -f "$data/agents/enabled.json" ]
[ -f "$data/agents/claude/agent.json" ]
[ -f "$data/agents/goose/icon.png" ]
[ ! -e "$home/.config/agent-pane/agents" ]

# The skill: one canonical copy, and an owned copy per skills folder.
skill="$data/skills/agent-pane/SKILL.md"
cmp "$root/skills/agent-pane/SKILL.md" "$skill"
for dir in "$home/.agents/skills/agent-pane" "$home/.claude/skills/agent-pane"; do
	cmp "$root/skills/agent-pane/SKILL.md" "$dir/SKILL.md"
	[ -f "$dir/.agent-pane-managed" ]
done
grep -qx 'name: agent-pane' "$skill"
grep -q '^description: ' "$skill"

# Doctor finds what is broken, and --fix puts it back without the network.
doctor >/dev/null
rm "$manifest"
echo stale >"$home/.agents/skills/agent-pane/SKILL.md"
echo junk >"$data/extensions/chrome/extra.js"
if doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with a missing manifest" >&2
	exit 1
fi
grep -q '^FAIL  manifest' "$tmp/doctor.out"
grep -q '^FAIL  skill copy' "$tmp/doctor.out"
grep -q '^FAIL  extension' "$tmp/doctor.out"
doctor --fix >/dev/null
[ -s "$manifest" ]
cmp "$root/skills/agent-pane/SKILL.md" "$home/.agents/skills/agent-pane/SKILL.md"
[ ! -e "$data/extensions/chrome/extra.js" ]
doctor >/dev/null
doctor --fix | grep -q 'nothing to fix'

# A binary with the right bytes but no execute bit is repaired too.
chmod 644 "$home/.local/bin/agent-pane"
if HOME="$home" "$data/release/agent-pane" doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with a non-executable binary" >&2
	exit 1
fi
grep -q '^FAIL  binary' "$tmp/doctor.out"
HOME="$home" "$data/release/agent-pane" doctor --fix >/dev/null
[ -x "$home/.local/bin/agent-pane" ]

# Updating replaces owned copies; folders that are not ours stay as they are.
echo extra >"$home/.claude/skills/agent-pane/old.md"
mkdir -p "$home/.agents/skills/mine"
echo mine >"$home/.agents/skills/mine/SKILL.md"
place >/dev/null
[ ! -e "$home/.claude/skills/agent-pane/old.md" ]
grep -qx mine "$home/.agents/skills/mine/SKILL.md"

# A run cut short leaves its staging folder; the next run still installs.
rm -rf "$home/.claude/skills/agent-pane"
mkdir -p "$data/skills/.agent-pane-copy"
echo partial >"$data/skills/.agent-pane-copy/SKILL.md"
place >/dev/null
cmp "$root/skills/agent-pane/SKILL.md" "$home/.claude/skills/agent-pane/SKILL.md"
[ -f "$home/.claude/skills/agent-pane/.agent-pane-managed" ]
[ ! -e "$data/skills/.agent-pane-copy" ]

rm -rf "$home/.claude/skills/agent-pane"
mkdir -p "$home/.claude/skills/agent-pane"
echo user >"$home/.claude/skills/agent-pane/SKILL.md"
rm -rf "$home/.agents/skills/agent-pane"
ln -s "$home/.agents/skills/mine" "$home/.agents/skills/agent-pane"
place >/dev/null 2>&1
grep -qx user "$home/.claude/skills/agent-pane/SKILL.md"
[ -L "$home/.agents/skills/agent-pane" ]
grep -qx mine "$home/.agents/skills/mine/SKILL.md"
doctor | grep -q '^warn  skill copy'

# A new release replaces the kept one.
echo '// new' >"$rel/extensions/chrome/new.js"
place >"$tmp/update.out" 2>/dev/null
grep -qx "agent-pane: updated ~/.local/share/agent-pane/release" "$tmp/update.out"
[ -f "$data/release/extensions/chrome/new.js" ]
[ -f "$data/extensions/chrome/new.js" ]

if [ "$(uname -s)" = Darwin ]; then
	plist="$home/Library/LaunchAgents/com.agentpane.spawn.plist"
	[ -s "$plist" ]
	grep -F "$home/.local/bin/agent-pane" "$plist" >/dev/null
	grep -F '<string>spawn</string>' "$plist" >/dev/null
fi

empty="$tmp/empty"
mkdir -p "$empty"
if HOME="$empty" sh "$rel/place.sh" >/dev/null 2>&1; then
	echo "place.sh succeeded with no browser directory" >&2
	exit 1
fi

# install.sh: the repository copy refuses to run.
if HOME="$empty" sh "$root/install.sh" >"$tmp/template.out" 2>&1; then
	echo "the install.sh template ran" >&2
	exit 1
fi
grep -q 'unreleased' "$tmp/template.out"

# install.sh as published: VERSION stamped like release.yml does, the base
# URL pointed at a local copy of a release.
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
*) arch=arm64 ;;
esac
os=$(uname -s | tr '[:upper:]' '[:lower:]')
releases="$tmp/releases"
archive="agent-pane-9.9.9-${os}_${arch}.tar.gz"
mkdir -p "$releases/v9.9.9"
rm -f "$rel/extensions/chrome/new.js"
(cd "$rel" && tar -czf "$releases/v9.9.9/$archive" .)
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$releases/v9.9.9" && sha256sum "$archive" >checksums.txt)
else
	(cd "$releases/v9.9.9" && shasum -a 256 "$archive" >checksums.txt)
fi
sed -e 's/^VERSION=""$/VERSION="v9.9.9"/' -e "s|^base=.*|base=\"file://$releases\"|" "$root/install.sh" >"$tmp/install.sh"
grep -qx 'VERSION="v9.9.9"' "$tmp/install.sh"

fresh="$tmp/fresh"
mkdir -p "$fresh/$(echo "$chrome" | sed "s|^$home/||")"
HOME="$fresh" sh "$tmp/install.sh" >"$tmp/install.out"
grep -q '^ok    binary' "$tmp/install.out"
cmp "$rel/agent-pane" "$fresh/.local/bin/agent-pane"
HOME="$fresh" sh "$tmp/install.sh" >"$tmp/install2.out"
if grep '^agent-pane: updated' "$tmp/install2.out"; then
	echo "a second install.sh run changed something" >&2
	exit 1
fi

# A checksum mismatch installs nothing.
bad="$tmp/bad"
mkdir -p "$bad/$(echo "$chrome" | sed "s|^$home/||")"
sed -i.orig 's/^[0-9a-f]*/0000000000000000000000000000000000000000000000000000000000000000/' "$releases/v9.9.9/checksums.txt"
if HOME="$bad" sh "$tmp/install.sh" >/dev/null 2>&1; then
	echo "install.sh accepted a bad checksum" >&2
	exit 1
fi
[ ! -e "$bad/.local" ]

echo "install_test: ok"
