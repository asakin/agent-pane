#!/bin/sh
# Tests install.sh, scripts/place.sh and `agent-pane doctor` against a release
# built from this checkout, in temporary HOMEs. Needs Go. Touches nothing in
# the real HOME. It downloads the pinned herdr-core build once, into
# bin/herdr-core/ like scripts/dev-install.sh, and nothing else.
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
platform="$(cd "$root" && go env GOOS)_$(cd "$root" && go env GOARCH)"
sh "$root/scripts/fetch-herdr-core.sh" "$platform" "$root/bin/herdr-core/$platform"
cp -R "$root/bin/herdr-core/$platform/." "$rel/"
# A fetch cut short leaves no lock, so the next run fetches again rather than
# keeping a partial file such as empty notices.
cache="$tmp/core-cache"
cp -R "$root/bin/herdr-core/$platform" "$cache"
rm "$cache/herdr-core.lock"
: >"$cache/licenses/herdr-core/THIRD-PARTY-NOTICES"
sh "$root/scripts/fetch-herdr-core.sh" "$platform" "$cache"
cmp "$rel/licenses/herdr-core/THIRD-PARTY-NOTICES" "$cache/licenses/herdr-core/THIRD-PARTY-NOTICES"
cmp "$rel/libexec/hc" "$cache/libexec/hc"

sum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# The lock: one line a platform, `file` lines only for the Windows build.
for p in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64; do
	grep -c "^$p " "$root/scripts/herdr-core.lock" | grep -qx 1
done
grep -c '^windows_amd64\.file ' "$root/scripts/herdr-core.lock" | grep -qx 2
[ "$(grep -c '^[a-z0-9_]*\.file ' "$root/scripts/herdr-core.lock")" = 2 ]

# fetch-herdr-core.sh against local stand-in builds, with a lock that has
# `file` lines: a Unix build next to them still fetches as before, and the
# Windows build is laid out on this host.
fake="$tmp/fake-core"
mkdir -p "$fake/scripts" "$fake/tar" "$fake/zip/conpty/x64" "$fake/zip/THIRD-PARTY-NOTICES"
cp "$root/scripts/fetch-herdr-core.sh" "$fake/scripts/"
printf 'unix core\n' >"$fake/tar/herdr-core"
printf 'license\n' >"$fake/tar/LICENSE"
printf 'notices\n' >"$fake/tar/THIRD-PARTY-NOTICES"
(cd "$fake/tar" && tar -czf "$fake/core.tar.gz" herdr-core LICENSE THIRD-PARTY-NOTICES)
printf 'windows core\n' >"$fake/zip/herdr-core.exe"
printf 'dll\n' >"$fake/zip/conpty/conpty.dll"
printf 'console\n' >"$fake/zip/conpty/x64/OpenConsole.exe"
printf 'license\n' >"$fake/zip/LICENSE"
printf 'core notices\n' >"$fake/zip/THIRD-PARTY-NOTICES/herdr-core.txt"
printf 'conpty notices\n' >"$fake/zip/THIRD-PARTY-NOTICES/conpty.txt"
printf 'extra\n' >"$fake/zip/README"
(cd "$fake/zip" && zip -qr "$fake/core.zip" .)
cat >"$fake/scripts/herdr-core.lock" <<EOF
# comment
version 0.0.1
generation 1
linux_amd64 file://$fake/core.tar.gz $(sum "$fake/core.tar.gz") $(sum "$fake/tar/herdr-core")
windows_amd64 file://$fake/core.zip $(sum "$fake/core.zip") $(sum "$fake/zip/herdr-core.exe")
windows_amd64.file conpty/conpty.dll $(sum "$fake/zip/conpty/conpty.dll")
windows_amd64.file conpty/x64/OpenConsole.exe $(sum "$fake/zip/conpty/x64/OpenConsole.exe")
EOF
fetch() {
	sh "$fake/scripts/fetch-herdr-core.sh" "$@"
}
fetch linux_amd64 "$fake/out-linux"
cmp "$fake/tar/herdr-core" "$fake/out-linux/libexec/hc"
[ -x "$fake/out-linux/libexec/hc" ]
[ ! -e "$fake/out-linux/libexec/conpty" ]
cmp "$fake/tar/THIRD-PARTY-NOTICES" "$fake/out-linux/licenses/herdr-core/THIRD-PARTY-NOTICES"
cmp "$fake/scripts/herdr-core.lock" "$fake/out-linux/herdr-core.lock"

win="$fake/out-windows"
fetch windows_amd64 "$win"
cmp "$fake/zip/herdr-core.exe" "$win/libexec/hc.exe"
cmp "$fake/zip/conpty/conpty.dll" "$win/libexec/conpty/conpty.dll"
cmp "$fake/zip/conpty/x64/OpenConsole.exe" "$win/libexec/conpty/x64/OpenConsole.exe"
cmp "$fake/zip/LICENSE" "$win/licenses/herdr-core/LICENSE"
cmp "$fake/zip/THIRD-PARTY-NOTICES/herdr-core.txt" "$win/licenses/herdr-core/THIRD-PARTY-NOTICES/herdr-core.txt"
cmp "$fake/zip/THIRD-PARTY-NOTICES/conpty.txt" "$win/licenses/herdr-core/THIRD-PARTY-NOTICES/conpty.txt"
cmp "$fake/scripts/herdr-core.lock" "$win/herdr-core.lock"
[ ! -e "$win/libexec/hc" ] && [ ! -e "$win/libexec/README" ] && [ ! -e "$win/libexec/herdr-core.exe" ]
[ -z "$(find "$fake" -name '.herdr-core.*')" ]
# A complete Windows folder is left as it is, without downloading.
mv "$fake/core.zip" "$fake/core.zip.away"
fetch windows_amd64 "$win"
# A changed ConPTY file is fetched again.
echo wrong >"$win/libexec/conpty/x64/OpenConsole.exe"
if fetch windows_amd64 "$win" 2>/dev/null; then
	echo "fetch kept a wrong ConPTY file without downloading" >&2
	exit 1
fi
mv "$fake/core.zip.away" "$fake/core.zip"
fetch windows_amd64 "$win"
cmp "$fake/zip/conpty/x64/OpenConsole.exe" "$win/libexec/conpty/x64/OpenConsole.exe"
# An archive whose ConPTY file does not match its file line installs nothing.
sed "s|^\(windows_amd64.file conpty/conpty.dll\) .*|\1 $(sum "$fake/zip/LICENSE")|" "$fake/scripts/herdr-core.lock" >"$fake/lock.new"
mv "$fake/lock.new" "$fake/scripts/herdr-core.lock"
if fetch windows_amd64 "$fake/out-bad" 2>"$fake/bad.err"; then
	echo "fetch accepted a ConPTY file that does not match the lock" >&2
	exit 1
fi
grep -q 'do not match the windows_amd64.file lines' "$fake/bad.err"
[ ! -e "$fake/out-bad/herdr-core.lock" ] && [ ! -e "$fake/out-bad/libexec/hc.exe" ]

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
# The bundled core is private: in the data folder, never on PATH.
cmp "$rel/libexec/hc" "$data/libexec/hc"
[ -x "$data/libexec/hc" ]
[ ! -e "$home/.local/bin/hc" ]
[ -s "$data/licenses/herdr-core/LICENSE" ]
[ -s "$data/licenses/herdr-core/THIRD-PARTY-NOTICES" ]
grep -q '^ok    core  ' "$tmp/first.out"
grep -q '^ok    core licenses' "$tmp/first.out"
grep -q '^ok    core isolation' "$tmp/first.out"
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

# A missing or wrong bundled core is reported and put back.
rm "$data/libexec/hc"
if doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with a missing core" >&2
	exit 1
fi
grep -q '^FAIL  core .*is missing' "$tmp/doctor.out"
echo wrong >"$data/libexec/hc"
chmod 755 "$data/libexec/hc"
rm "$data/licenses/herdr-core/LICENSE"
if doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with a wrong core" >&2
	exit 1
fi
grep -q '^FAIL  core .*not the pinned herdr-core' "$tmp/doctor.out"
grep -q '^FAIL  core licenses' "$tmp/doctor.out"
doctor --fix >/dev/null
cmp "$rel/libexec/hc" "$data/libexec/hc"
doctor >/dev/null

# The bundled core on PATH is a problem; a person's own hc is not touched.
ln -s "$data/libexec/hc" "$home/.local/bin/hc"
if doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with the bundled core on PATH" >&2
	exit 1
fi
grep -q '^FAIL  core isolation' "$tmp/doctor.out"
rm "$home/.local/bin/hc"
ln -s "$data/release/libexec/hc" "$home/.local/bin/hc"
if doctor >"$tmp/doctor.out" 2>&1; then
	echo "doctor passed with the release's core on PATH" >&2
	exit 1
fi
grep -q '^FAIL  core isolation' "$tmp/doctor.out"
rm "$home/.local/bin/hc"
printf '#!/bin/sh\necho mine\n' >"$home/.local/bin/hc"
chmod 755 "$home/.local/bin/hc"
place >/dev/null
grep -qx 'echo mine' "$home/.local/bin/hc"
doctor | grep -q '^ok    core isolation.*separate hc'
rm "$home/.local/bin/hc"

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
