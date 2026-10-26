#!/bin/sh
# Installs agent-terminal. Safe to run again: it writes the same files again.
# Exits non-zero if the native-messaging manifest was not written.
set -eu

fail() {
	echo "agent-terminal: $*" >&2
	exit 1
}

home=${HOME:?}
bin_dir="$home/.local/bin"
dest="$bin_dir/agent-terminal"
data_dir="$home/.local/share/agent-terminal"
host="$data_dir/native_messaging_host"
config_dir="$home/.config/agent-terminal"
repo="asakin/agent-terminal"

sh_quote() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

# Replace dest by a new inode. Copying onto a running Mach-O makes macOS
# kill the next exec with a codesign failure.
place_binary() {
	src=$1
	staged=$(mktemp "$bin_dir/.agent-terminal.XXXXXX") || fail "could not create a temporary binary"
	cp "$src" "$staged" || fail "could not copy the binary"
	chmod 755 "$staged" || fail "could not make the binary executable"
	mv -f "$staged" "$dest" || fail "could not install the binary"
}

install_binary() {
	mkdir -p "$bin_dir"
	if [ -n "${AGENT_TERMINAL_BIN:-}" ]; then
		[ -f "$AGENT_TERMINAL_BIN" ] || fail "AGENT_TERMINAL_BIN is not a file: $AGENT_TERMINAL_BIN"
		place_binary "$AGENT_TERMINAL_BIN"
	else
		os=$(uname -s | tr '[:upper:]' '[:lower:]')
		arch=$(uname -m)
		case "$arch" in
		x86_64 | amd64) arch=amd64 ;;
		arm64 | aarch64) arch=arm64 ;;
		*) fail "unsupported architecture: $arch" ;;
		esac
		case "$os" in
		darwin | linux) ;;
		*) fail "unsupported operating system: $os" ;;
		esac

		json=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest") || fail "could not read the latest release"
		url=$(printf '%s\n' "$json" | sed -n 's/.*"browser_download_url": *"\([^"]*\)".*/\1/p' | grep -F "${os}_${arch}.tar.gz" | head -n 1 || true)
		[ -n "$url" ] || fail "the latest release has no ${os}_${arch} archive"
		tmp=$(mktemp -d)
		curl -fsSL -o "$tmp/archive.tar.gz" "$url" || fail "download failed: $url"
		tar -xzf "$tmp/archive.tar.gz" -C "$tmp" || fail "could not unpack $url"
		[ -f "$tmp/agent-terminal" ] || fail "archive has no agent-terminal binary"
		place_binary "$tmp/agent-terminal"
		if [ -d "$tmp/extensions/chrome" ]; then
			rm -rf "$data_dir/extensions/chrome"
			mkdir -p "$data_dir/extensions"
			cp -R "$tmp/extensions/chrome" "$data_dir/extensions/chrome"
		fi
		rm -rf "$tmp"
	fi
	[ -s "$dest" ] && [ -x "$dest" ] || fail "binary was not installed at $dest"
}

write_host() {
	mkdir -p "$data_dir"
	{
		echo "#!/bin/sh"
		echo "exec $(sh_quote "$dest") serve"
	} >"$host"
	chmod 755 "$host"
	[ -s "$host" ] && [ -x "$host" ] || fail "host wrapper was not written: $host"
}

write_manifest() {
	parent=$1
	dir=$2
	kind=$3
	if [ ! -d "$parent" ]; then
		return 0
	fi
	mkdir -p "$dir"
	rm -f "$dir/com.github.pomdtr.tweety.json"
	path="$dir/com.agentterminal.native.json"
	escaped=$(json_escape "$host")
	if [ "$kind" = "chromium" ]; then
		cat >"$path" <<EOF
{
    "name": "com.agentterminal.native",
    "description": "An integrated terminal for your agent in your browser",
    "type": "stdio",
    "path": "${escaped}",
    "allowed_origins": [
        "chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/"
    ]
}
EOF
	else
		cat >"$path" <<EOF
{
    "name": "com.agentterminal.native",
    "description": "An integrated terminal for your agent in your browser",
    "type": "stdio",
    "path": "${escaped}",
    "allowed_extensions": [
        "agent-terminal@agent-terminal.local"
    ]
}
EOF
	fi
	[ -s "$path" ] || fail "manifest was not written: $path"
	grep -F "$host" "$path" >/dev/null || fail "manifest does not point at the host: $path"
	count=$((count + 1))
	echo "agent-terminal: wrote $path"
}

write_manifests() {
	count=0
	case "$(uname -s)" in
	Darwin)
		support="$home/Library/Application Support"
		write_manifest "$support/Google/Chrome" "$support/Google/Chrome/NativeMessagingHosts" chromium
		write_manifest "$support/Chromium" "$support/Chromium/NativeMessagingHosts" chromium
		write_manifest "$support/BraveSoftware/Brave-Browser" "$support/BraveSoftware/Brave-Browser/NativeMessagingHosts" chromium
		write_manifest "$support/Vivaldi" "$support/Vivaldi/NativeMessagingHosts" chromium
		write_manifest "$support/net.imput.helium" "$support/net.imput.helium/NativeMessagingHosts" chromium
		write_manifest "$support/Microsoft/Edge" "$support/Microsoft/Edge/NativeMessagingHosts" chromium
		write_manifest "$support/Mozilla" "$support/Mozilla/NativeMessagingHosts" gecko
		write_manifest "$support/zen" "$support/zen/NativeMessagingHosts" gecko
		;;
	Linux)
		write_manifest "$home/.config/google-chrome" "$home/.config/google-chrome/native-messaging-hosts" chromium
		write_manifest "$home/.config/chromium" "$home/.config/chromium/native-messaging-hosts" chromium
		write_manifest "$home/.config/microsoft-edge" "$home/.config/microsoft-edge/native-messaging-hosts" chromium
		write_manifest "$home/.config/brave" "$home/.config/brave/native-messaging-hosts" chromium
		write_manifest "$home/.config/vivaldi" "$home/.config/vivaldi/native-messaging-hosts" chromium
		write_manifest "$home/.config/helium" "$home/.config/helium/native-messaging-hosts" chromium
		write_manifest "$home/.mozilla" "$home/.mozilla/native-messaging-hosts" gecko
		write_manifest "$home/.config/zen" "$home/.config/zen/native-messaging-hosts" gecko
		;;
	*)
		fail "unsupported operating system: $(uname -s)"
		;;
	esac
	[ "$count" -gt 0 ] || fail "no browser config directory was found, so no native messaging manifest was installed"
}

ensure_path() {
	rc="$home/.zshrc"
	case "${SHELL:-}" in
	*/bash) rc="$home/.bashrc" ;;
	*/zsh) rc="$home/.zshrc" ;;
	esac
	if [ -f "$rc" ] && grep -q '# agent-terminal:path' "$rc"; then
		return 0
	fi
	printf '\n# agent-terminal:path\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$rc"
	echo "agent-terminal: added ~/.local/bin to PATH in $rc"
}

agents_body() {
	cat <<'EOF'
<!-- agent-terminal:start -->
# Agent Terminal

You are in the Agent Terminal side panel: a local shell, not a browser page.
Browser automation is whatever CLI you already run, such as Playwright or Playwriter. This extension does not attach to a browser and does not choose one.
<!-- agent-terminal:end -->
EOF
}

claude_body() {
	cat <<'EOF'
<!-- agent-terminal:start -->
@AGENTS.md
<!-- agent-terminal:end -->
EOF
}

upsert_managed() {
	file=$1
	body=$2
	if [ ! -e "$file" ]; then
		printf '%s\n' "$body" >"$file"
		return 0
	fi
	if [ -L "$file" ]; then
		echo "agent-terminal: $file is a symlink; left unchanged" >&2
		return 0
	fi
	if ! grep -q '<!-- agent-terminal:start -->' "$file"; then
		echo "agent-terminal: $file has no Agent Terminal block; left unchanged" >&2
		return 0
	fi
	if ! grep -q '<!-- agent-terminal:end -->' "$file"; then
		fail "$file has a start marker and no end marker"
	fi
	tmp=$(mktemp)
	printf '%s\n' "$body" >"$tmp.body"
	awk '
		BEGIN { replaced = 0 }
		$0 == "<!-- agent-terminal:start -->" && replaced == 0 {
			while ((getline line < body) > 0) print line
			close(body)
			replaced = 1
			next
		}
		replaced == 1 && $0 != "<!-- agent-terminal:end -->" { next }
		replaced == 1 && $0 == "<!-- agent-terminal:end -->" { replaced = 0; next }
		{ print }
	' body="$tmp.body" "$file" >"$tmp"
	mv "$tmp" "$file"
	rm -f "$tmp.body"
}

write_instructions() {
	mkdir -p "$config_dir"
	body=$(agents_body)
	printf '%s\n' "$body" >"$config_dir/AGENTS.md"
	[ -s "$config_dir/AGENTS.md" ] || fail "could not write $config_dir/AGENTS.md"
	upsert_managed "$home/AGENTS.md" "$body"
	upsert_managed "$home/CLAUDE.md" "$(claude_body)"
}

xml_escape() {
	printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'
}

write_launchd() {
	[ "$(uname -s)" = Darwin ] || return 0
	plist="$home/Library/LaunchAgents/com.agentterminal.spawn.plist"
	mkdir -p "$(dirname "$plist")"
	cat >"$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.agentterminal.spawn</string>
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
	[ -s "$plist" ] || fail "could not write $plist"
	if [ -n "${AGENT_TERMINAL_SKIP_LAUNCHD:-}" ]; then
		return 0
	fi
	domain="gui/$(id -u)"
	target="$domain/com.agentterminal.spawn"
	launchctl bootout "$target" 2>/dev/null || true
	# bootout returns before the job is gone. bootstrap during that window
	# fails with "Bootstrap failed: 5: Input/output error".
	n=0
	while launchctl print "$target" >/dev/null 2>&1; do
		n=$((n + 1))
		if [ "$n" -ge 20 ]; then
			launchctl kickstart -k "$target" || fail "could not start the spawn agent"
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
}

install_binary
write_host
write_manifests
write_launchd
ensure_path
write_instructions
echo "agent-terminal: installed $dest"
if [ -d "$data_dir/extensions/chrome" ]; then
	echo "agent-terminal: load the extension from $data_dir/extensions/chrome"
fi
