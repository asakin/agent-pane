#!/bin/sh
# Lays out the pinned herdr-core build for one platform the way a release
# archive carries it:
#
#   <dir>/libexec/hc  <dir>/licenses/herdr-core/  <dir>/herdr-core.lock
#
#   sh scripts/fetch-herdr-core.sh <goos>_<goarch> <dir>
#
# The build is the one scripts/herdr-core.lock pins. The download and the
# binary in it are checked against the lock's checksums; a <dir> that already
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

mkdir -p "$out/libexec" "$out/licenses/herdr-core"
if [ -f "$out/libexec/hc" ] && [ "$(sha256 "$out/libexec/hc")" = "$binary_sha" ] &&
	[ -s "$out/licenses/herdr-core/LICENSE" ] && [ -s "$out/licenses/herdr-core/THIRD-PARTY-NOTICES" ] &&
	cmp -s "$lock" "$out/herdr-core.lock"; then
	exit 0
fi

# Next to <dir>, on the same file system, so mv below is a rename.
tmp=$(mktemp -d "$(dirname "$out")/.herdr-core.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
# Give up on a download that stalls (under 1 KiB/s for a minute) or runs past
# ten minutes, instead of waiting forever.
curl -fsSL --connect-timeout 30 --speed-limit 1024 --speed-time 60 --max-time 600 \
	-o "$tmp/core.tar.gz" "$url" || fail "download failed: $url"
[ "$(sha256 "$tmp/core.tar.gz")" = "$archive_sha" ] || fail "checksum mismatch for $url"
tar -xzf "$tmp/core.tar.gz" -C "$tmp" herdr-core LICENSE THIRD-PARTY-NOTICES || fail "$url does not hold herdr-core, LICENSE and THIRD-PARTY-NOTICES"
[ "$(sha256 "$tmp/herdr-core")" = "$binary_sha" ] || fail "the herdr-core binary in $url does not match scripts/herdr-core.lock"

chmod 755 "$tmp/herdr-core"
# The lock goes last and marks a complete <dir>: without it, the check above
# fails and the next run fetches again.
rm -f "$out/herdr-core.lock"
mv -f "$tmp/herdr-core" "$out/libexec/hc"
mv -f "$tmp/LICENSE" "$tmp/THIRD-PARTY-NOTICES" "$out/licenses/herdr-core/"
cp "$lock" "$tmp/herdr-core.lock"
mv -f "$tmp/herdr-core.lock" "$out/herdr-core.lock"
