#!/bin/sh
# Lays out the pinned herdr-core build for one platform the way a release
# archive carries it:
#
#   <dir>/libexec/hc  <dir>/licenses/herdr-core/  <dir>/herdr-core.lock
#
# or, for a Windows build (a .zip URL), with the ConPTY files where the binary
# looks for them and the notices as a folder:
#
#   <dir>/libexec/hc.exe  <dir>/libexec/conpty/conpty.dll
#   <dir>/libexec/conpty/x64/OpenConsole.exe  <dir>/licenses/herdr-core/LICENSE
#   <dir>/licenses/herdr-core/THIRD-PARTY-NOTICES/  <dir>/herdr-core.lock
#
#   sh scripts/fetch-herdr-core.sh <goos>_<goarch> <dir>
#
# Any platform's build can be fetched on any host, so a Linux runner lays out
# the Windows build too. The build is the one scripts/herdr-core.lock pins. The
# download, the binary in it and each file the lock's `<goos>_<goarch>.file`
# lines name are checked against the lock's checksums; a <dir> that already
# holds the pinned build is left as it is, so nothing is downloaded again.
# Each file is staged next to <dir> and renamed into place, so a run cut short,
# or a second fetch running at the same time, never leaves a partial file there.
set -eu

fail() {
	echo "fetch-herdr-core: $*" >&2
	exit 1
}

[ $# -eq 2 ] || fail "usage: sh scripts/fetch-herdr-core.sh <goos>_<goarch> <dir>"
platform=$1
out=$2
lock=$(CDPATH= cd -- "$(dirname "$0")" && pwd)/herdr-core.lock

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

entry=$(awk -v p="$platform" '$1 == p { print $2, $3, $4 }' "$lock")
[ -n "$entry" ] || fail "scripts/herdr-core.lock has no build for $platform"
set -- $entry
url=$1
archive_sha=$2
binary_sha=$3
# The build's other files, one "<path> <sha256>" a line, each path relative to
# the binary and the same in the archive and in <dir>/libexec.
files=$(awk -v p="$platform.file" '$1 == p { print $2, $3 }' "$lock")
for path in $(echo "$files" | cut -d' ' -f1); do
	case /$path/ in
	//* | */../*) fail "scripts/herdr-core.lock: $platform.file $path is not a path inside the build" ;;
	esac
done

# files_match <dir>: <dir> holds each file the lock's file lines name, with its
# pinned checksum.
files_match() {
	[ -n "$files" ] || return 0
	echo "$files" | while read -r path sha; do
		[ -f "$1/$path" ] && [ "$(sha256 "$1/$path")" = "$sha" ] || exit 1
	done
}

# notices_ok <path>: the third-party notices are a non-empty file or a folder
# of non-empty files.
notices_ok() {
	if [ -d "$1" ]; then
		[ -n "$(find "$1" -type f)" ] && [ -z "$(find "$1" -type f -size 0)" ]
	else
		[ -s "$1" ]
	fi
}

case $url in
*.zip)
	kind=zip
	member=herdr-core.exe
	hc=hc.exe
	;;
*)
	kind=tar.gz
	member=herdr-core
	hc=hc
	;;
esac

mkdir -p "$out/libexec" "$out/licenses/herdr-core"
if [ -f "$out/libexec/$hc" ] && [ "$(sha256 "$out/libexec/$hc")" = "$binary_sha" ] &&
	files_match "$out/libexec" &&
	[ -s "$out/licenses/herdr-core/LICENSE" ] && notices_ok "$out/licenses/herdr-core/THIRD-PARTY-NOTICES" &&
	cmp -s "$lock" "$out/herdr-core.lock"; then
	exit 0
fi

# Next to <dir>, on the same file system, so mv below is a rename.
tmp=$(mktemp -d "$(dirname "$out")/.herdr-core.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
# Give up on a download that stalls (under 1 KiB/s for a minute) or runs past
# ten minutes, instead of waiting forever.
curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 --max-time 600 \
	-o "$tmp/core.$kind" "$url" || fail "download failed: $url"
[ "$(sha256 "$tmp/core.$kind")" = "$archive_sha" ] || fail "checksum mismatch for $url"
mkdir "$tmp/x"
if [ $kind = zip ]; then
	command -v unzip >/dev/null 2>&1 || fail "unpacking $url needs unzip"
	# The file paths are checked above and hold no spaces.
	unzip -q "$tmp/core.zip" herdr-core.exe LICENSE 'THIRD-PARTY-NOTICES/*' $(echo "$files" | cut -d' ' -f1) -d "$tmp/x" ||
		fail "$url does not hold herdr-core.exe, LICENSE, THIRD-PARTY-NOTICES/ and the files scripts/herdr-core.lock names"
else
	tar -xzf "$tmp/core.tar.gz" -C "$tmp/x" herdr-core LICENSE THIRD-PARTY-NOTICES || fail "$url does not hold herdr-core, LICENSE and THIRD-PARTY-NOTICES"
fi
[ "$(sha256 "$tmp/x/$member")" = "$binary_sha" ] || fail "the herdr-core binary in $url does not match scripts/herdr-core.lock"
files_match "$tmp/x" || fail "the files in $url do not match the $platform.file lines in scripts/herdr-core.lock"
notices_ok "$tmp/x/THIRD-PARTY-NOTICES" || fail "$url has empty third-party notices"

chmod 755 "$tmp/x/$member"
# The lock goes last and marks a complete <dir>: without it, the check above
# fails and the next run fetches again.
rm -f "$out/herdr-core.lock"
mv -f "$tmp/x/$member" "$out/libexec/$hc"
if [ -n "$files" ]; then
	echo "$files" | while read -r path sha; do
		mkdir -p "$out/libexec/$(dirname "$path")"
		mv -f "$tmp/x/$path" "$out/libexec/$path"
	done
fi
# mv does not replace a folder of notices, so the old ones go first.
rm -rf "$out/licenses/herdr-core/THIRD-PARTY-NOTICES"
mv -f "$tmp/x/LICENSE" "$tmp/x/THIRD-PARTY-NOTICES" "$out/licenses/herdr-core/"
cp "$lock" "$tmp/herdr-core.lock"
mv -f "$tmp/herdr-core.lock" "$out/herdr-core.lock"
