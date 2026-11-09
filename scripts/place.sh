#!/bin/sh
# Puts an Agent Pane release in place on this machine. The release is the
# folder this script sits in, laid out like a release archive:
#
#   agent-pane  place.sh  instructions.md  extensions/chrome/  agents/
#   skills/agent-pane/
#
# install.sh runs it from a downloaded archive, scripts/dev-install.sh from a
# checkout build, and `agent-pane doctor --fix` from the installed copy in
# ~/.local/share/agent-pane/release. It never downloads anything.
#
# Every step compares before it writes, so running it again changes nothing
# that is already right and prints a line only for what it changed. It ends
# with `agent-pane doctor` and fails when the doctor finds a problem.
set -eu

fail() {
	echo "agent-pane: $*" >&2
	exit 1
}

# updated reports a change, with paths under HOME shown as ~/.
updated() {
	what=$*
	case "$what" in
	"$home"/*) echo "agent-pane: updated ~/${what#"$home"/}" ;;
	*) echo "agent-pane: updated $what" ;;
	esac
}

home=${HOME:?}
bin_dir="$home/.local/bin"
dest="$bin_dir/agent-pane"
data_dir="$home/.local/share/agent-pane"
release="$data_dir/release"
host="$data_dir/native_messaging_host"
config_dir="$home/.config/agent-pane"
extension_id="eakooboihfgnikdhdldcmoiafgioeglm"
skill_marker=".agent-pane-managed"
# Present from the moment the binary or plist changes until the spawn agent has
# been restarted on them, so a run cut short in between restarts it next time.
restart_pending="$data_dir/spawn-restart-pending"

sh_quote() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

xml_escape() {
	printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'
}

# same_tree reports whether two folders hold the same files, ignoring the
# skill ownership marker.
same_tree() {
	[ -d "$1" ] && [ -d "$2" ] && diff -r -q -x "$skill_marker" "$1" "$2" >/dev/null 2>&1
}

# write_if_changed writes stdin to $1 when the content differs, and returns 0
# only when it wrote. When $2 is given, that file is created before $1 is
# replaced, so a run cut short after the replacement still finds it. Feed it with a redirection, not a pipe: in a pipeline it
# runs in a subshell, where fail would not stop the script.
write_if_changed() {
	tmp_file=$(mktemp "$(dirname "$1")/.agent-pane.XXXXXX") || fail "could not write in $(dirname "$1")"
	# Callers sit in an if, where set -e is off: check the write explicitly so
	# a partial copy never replaces the file.
	if ! cat >"$tmp_file"; then
		rm -f "$tmp_file"
		fail "could not write $1"
	fi
	if [ -f "$1" ] && [ ! -L "$1" ] && cmp -s "$tmp_file" "$1"; then
		rm -f "$tmp_file"
		return 1
	fi
	if [ -n "${2:-}" ]; then
		: >"$2" || fail "could not write $2"
	fi
	mv -f "$tmp_file" "$1" || fail "could not write $1"
	return 0
}

check_layout() {
	src=$1
	[ -f "$src/agent-pane" ] || fail "$src has no agent-pane binary"
	[ -f "$src/place.sh" ] || fail "$src has no place.sh"
	[ -f "$src/agents/enabled.json" ] || fail "$src has no agent catalog"
	[ -s "$src/skills/agent-pane/SKILL.md" ] || fail "$src has no agent-pane skill"
	[ -s "$src/instructions.md" ] || fail "$src has no instructions.md"
	[ -f "$src/extensions/chrome/manifest.json" ] || fail "$src has no Chrome extension"
}

# keep_release makes $release a copy of the folder this script came from, so
# `agent-pane doctor` can compare against it and `doctor --fix` can rerun it.
keep_release() {
	src=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
	check_layout "$src"
	mkdir -p "$data_dir"
	# spawn.sock lives here; only this user may reach it (AGENTS.md).
	chmod 700 "$data_dir" || fail "could not restrict $data_dir"
	if [ -d "$release" ] && [ "$src" = "$(CDPATH= cd -- "$release" && pwd -P)" ]; then
		return 0
	fi
	if same_tree "$src" "$release"; then
		return 0
	fi
	rm -rf "$release.new" "$release.old"
	cp -R "$src" "$release.new" || fail "could not copy the release to $release"
	if [ -d "$release" ]; then
		mv "$release" "$release.old"
	fi
	mv "$release.new" "$release" || fail "could not install the release at $release"
	rm -rf "$release.old"
	updated "$release"
}

# Replace dest by a new inode. Copying onto a running Mach-O makes macOS
# kill the next exec with a codesign failure.
place_binary() {
	mkdir -p "$bin_dir"
	if [ -f "$dest" ] && [ -x "$dest" ] && cmp -s "$release/agent-pane" "$dest"; then
		return 0
	fi
	if [ "$(uname -s)" = Darwin ]; then
		: >"$restart_pending"
	fi
	staged=$(mktemp "$bin_dir/.agent-pane.XXXXXX") || fail "could not create a temporary binary"
	cp "$release/agent-pane" "$staged" || fail "could not copy the binary"
	chmod 755 "$staged" || fail "could not make the binary executable"
	mv -f "$staged" "$dest" || fail "could not install the binary"
	updated "$dest"
}

write_host() {
	if write_if_changed "$host" <<EOF; then
#!/bin/sh
exec $(sh_quote "$dest") serve
EOF
		updated "$host"
	fi
	chmod 755 "$host"
}

write_manifest() {
	parent=$1
	dir=$2
	if [ ! -d "$parent" ]; then
		return 0
	fi
	count=$((count + 1))
	mkdir -p "$dir"
	if [ -e "$dir/com.github.pomdtr.tweety.json" ]; then
		rm -f "$dir/com.github.pomdtr.tweety.json"
	fi
	path="$dir/com.agentpane.native.json"
	if write_if_changed "$path" <<EOF; then
{
    "name": "com.agentpane.native",
    "description": "Claude Code, Codex and Pi in a real terminal, in Chrome's side panel.",
    "type": "stdio",
    "path": "$(json_escape "$host")",
    "allowed_origins": [
        "chrome-extension://${extension_id}/"
    ]
}
EOF
		updated "$path"
	fi
}

# The browser list is repeated in internal/cmd/doctor.go; change both.
write_manifests() {
	count=0
	case "$(uname -s)" in
	Darwin)
		support="$home/Library/Application Support"
		write_manifest "$support/Google/Chrome" "$support/Google/Chrome/NativeMessagingHosts"
		write_manifest "$support/Chromium" "$support/Chromium/NativeMessagingHosts"
		write_manifest "$support/BraveSoftware/Brave-Browser" "$support/BraveSoftware/Brave-Browser/NativeMessagingHosts"
		write_manifest "$support/Vivaldi" "$support/Vivaldi/NativeMessagingHosts"
		write_manifest "$support/net.imput.helium" "$support/net.imput.helium/NativeMessagingHosts"
		write_manifest "$support/Microsoft/Edge" "$support/Microsoft/Edge/NativeMessagingHosts"
		;;
	Linux)
		write_manifest "$home/.config/google-chrome" "$home/.config/google-chrome/native-messaging-hosts"
		write_manifest "$home/.config/chromium" "$home/.config/chromium/native-messaging-hosts"
		write_manifest "$home/.config/microsoft-edge" "$home/.config/microsoft-edge/native-messaging-hosts"
		write_manifest "$home/.config/brave" "$home/.config/brave/native-messaging-hosts"
		write_manifest "$home/.config/vivaldi" "$home/.config/vivaldi/native-messaging-hosts"
		write_manifest "$home/.config/helium" "$home/.config/helium/native-messaging-hosts"
		;;
	*)
		fail "unsupported operating system: $(uname -s)"
		;;
	esac
	[ "$count" -gt 0 ] || fail "no browser config directory was found, so no native messaging manifest was installed"
}

# The spawn agent restarts only when its binary or plist changed since it was
# last started (restart_pending) or it is not loaded: a restart ends the shells
# open in pane tabs. The plist text is repeated in internal/cmd/doctor.go
# spawnAgentPlist; change both.
write_launchd() {
	[ "$(uname -s)" = Darwin ] || return 0
	plist="$home/Library/LaunchAgents/com.agentpane.spawn.plist"
	mkdir -p "$(dirname "$plist")"
	if write_if_changed "$plist" "$restart_pending" <<EOF; then
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.agentpane.spawn</string>
	<key>ProgramArguments</key>
	<array>
		<string>$(xml_escape "$dest")</string>
		<string>spawn</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
EOF
		updated "$plist"
	fi
	if [ -n "${AGENT_PANE_SKIP_LAUNCHD:-}" ]; then
		rm -f "$restart_pending"
		return 0
	fi
	domain="gui/$(id -u)"
	target="$domain/com.agentpane.spawn"
	if [ ! -e "$restart_pending" ] && launchctl print "$target" >/dev/null 2>&1; then
		return 0
	fi
	launchctl bootout "$target" 2>/dev/null || true
	# bootout returns before the job is gone. bootstrap during that window
	# fails with "Bootstrap failed: 5: Input/output error".
	n=0
	while launchctl print "$target" >/dev/null 2>&1; do
		n=$((n + 1))
		if [ "$n" -ge 20 ]; then
			launchctl kickstart -k "$target" || fail "could not start the spawn agent"
			rm -f "$restart_pending"
			updated "spawn agent (restarted)"
			return 0
		fi
		sleep 0.1
	done
	n=0
	while ! launchctl bootstrap "$domain" "$plist"; do
		n=$((n + 1))
		if [ "$n" -ge 8 ]; then
			fail "could not start the spawn agent"
		fi
		sleep 0.25
	done
	rm -f "$restart_pending"
	updated "spawn agent (restarted)"
}

ensure_path() {
	rc="$home/.zshrc"
	case "${SHELL:-}" in
	*/bash) rc="$home/.bashrc" ;;
	*/zsh) rc="$home/.zshrc" ;;
	esac
	if [ -f "$rc" ] && grep -q '# agent-pane:path' "$rc"; then
		return 0
	fi
	printf '\n# agent-pane:path\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$rc"
	updated "$rc (added ~/.local/bin to PATH)"
}

claude_body() {
	cat <<'EOF'
<!-- agent-pane:start -->
@AGENTS.md
<!-- agent-pane:end -->
EOF
}

# upsert_managed replaces the Agent Pane block in a file the person owns, and
# creates the file when it does not exist.
upsert_managed() {
	file=$1
	body=$2
	if [ ! -e "$file" ]; then
		printf '%s\n' "$body" >"$file"
		updated "$file"
		return 0
	fi
	if [ -L "$file" ]; then
		echo "agent-pane: $file is a symlink; left unchanged" >&2
		return 0
	fi
	if ! grep -q '<!-- agent-pane:start -->' "$file"; then
		echo "agent-pane: $file has no Agent Pane block; left unchanged" >&2
		return 0
	fi
	if ! grep -q '<!-- agent-pane:end -->' "$file"; then
		fail "$file has a start marker and no end marker"
	fi
	body_file=$(mktemp)
	out_file=$(mktemp)
	printf '%s\n' "$body" >"$body_file"
	awk '
		BEGIN { replaced = 0 }
		$0 == "<!-- agent-pane:start -->" && replaced == 0 {
			while ((getline line < body) > 0) print line
			close(body)
			replaced = 1
			next
		}
		replaced == 1 && $0 != "<!-- agent-pane:end -->" { next }
		replaced == 1 && $0 == "<!-- agent-pane:end -->" { replaced = 0; next }
		{ print }
	' body="$body_file" "$file" >"$out_file"
	if write_if_changed "$file" <"$out_file"; then
		updated "$file"
	fi
	rm -f "$body_file" "$out_file"
}

write_instructions() {
	mkdir -p "$config_dir"
	if write_if_changed "$config_dir/AGENTS.md" <"$release/instructions.md"; then
		updated "$config_dir/AGENTS.md"
	fi
	body=$(cat "$release/instructions.md")
	upsert_managed "$home/AGENTS.md" "$body"
	upsert_managed "$home/CLAUDE.md" "$(claude_body)"
}

# sync_tree makes $2 a copy of $1, swapping in a complete copy.
sync_tree() {
	if same_tree "$1" "$2"; then
		return 1
	fi
	rm -rf "$2.new"
	mkdir -p "$(dirname "$2")"
	cp -R "$1" "$2.new" || fail "could not copy $1"
	rm -rf "$2"
	mv "$2.new" "$2" || fail "could not install $2"
	return 0
}

sync_agents() {
	if sync_tree "$release/agents" "$data_dir/agents"; then
		updated "$data_dir/agents"
	fi
}

sync_extension() {
	if sync_tree "$release/extensions/chrome" "$data_dir/extensions/chrome"; then
		updated "$data_dir/extensions/chrome"
	fi
}

# The canonical skill lives in $data_dir/skills/agent-pane. Each coding agent
# gets its own copy where it looks for user skills; a copy is ours when it
# holds the marker file, and only ours are replaced.
sync_skill() {
	canonical="$data_dir/skills/agent-pane"
	if sync_tree "$release/skills/agent-pane" "$canonical"; then
		updated "$canonical"
	fi
	for skills in "$home/.agents/skills" "$home/.claude/skills"; do
		install_skill_copy "$canonical" "$skills/agent-pane"
	done
}

install_skill_copy() {
	from=$1
	to=$2
	tmp_copy="$data_dir/skills/.agent-pane-copy"
	rm -rf "$tmp_copy"
	if [ -L "$to" ] || { [ -e "$to" ] && [ ! -f "$to/$skill_marker" ]; }; then
		echo "agent-pane: $to is not Agent Pane's; left unchanged" >&2
		return 0
	fi
	if same_tree "$from" "$to"; then
		return 0
	fi
	mkdir -p "$(dirname "$to")"
	# Build the copy in the data folder, where no agent looks for skills,
	# marker first, then swap it in. A run cut short leaves the old marked
	# copy, or no copy, and never an unmarked one, so the next run repairs it.
	mkdir -p "$tmp_copy"
	printf 'Installed by Agent Pane; replaced on every install. Delete this folder to remove the skill.\n' >"$tmp_copy/$skill_marker"
	cp -R "$from/." "$tmp_copy/"
	rm -rf "$to"
	mv "$tmp_copy" "$to"
	updated "$to"
}

keep_release
place_binary
write_host
write_manifests
write_launchd
ensure_path
write_instructions
sync_agents
sync_skill
sync_extension

"$dest" doctor || fail "the installation has problems; see above"
installed=$("$dest" --version)
echo "agent-pane: installed $installed"
# A source build (scripts/dev-install.sh) loads extension/dist/chrome-mv3
# instead and prints its own note.
case "$installed" in
*" dev") ;;
*) echo "agent-pane: in Chrome, load the extension once with Load unpacked from ~/.local/share/agent-pane/extensions/chrome (chrome://extensions, Developer mode); after an update, click its reload button" ;;
esac
