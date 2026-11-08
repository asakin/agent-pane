#!/bin/sh
# Installs or updates Agent Pane from one GitHub release.
#
#   curl -fsSL https://github.com/asakin/agent-pane/releases/latest/download/install.sh | sh
#
# The release workflow publishes this script with VERSION set to the release's
# tag, so each published copy installs the release it came with. It downloads
# that release's archive, checks it against the release's checksums.txt, and
# runs the archive's place.sh. Running it again is safe.
#
# This copy in the repository has no VERSION and refuses to run. To install
# from a checkout, use scripts/dev-install.sh.
set -eu

VERSION=""
base="https://github.com/asakin/agent-pane/releases/download"

fail() {
	echo "agent-pane: $*" >&2
	exit 1
}

[ -n "$VERSION" ] || fail "this is the unreleased install.sh template. Install a release with:
  curl -fsSL https://github.com/asakin/agent-pane/releases/latest/download/install.sh | sh
or build from a checkout with scripts/dev-install.sh"

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

archive="agent-pane-${VERSION#v}-${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "agent-pane: downloading $VERSION for ${os}_${arch}"
curl -fsSL -o "$tmp/$archive" "$base/$VERSION/$archive" || fail "download failed: $base/$VERSION/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/$VERSION/checksums.txt" || fail "download failed: $base/$VERSION/checksums.txt"

grep "  $archive\$" "$tmp/checksums.txt" >"$tmp/archive.sha256" || fail "checksums.txt has no entry for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	(cd "$tmp" && sha256sum -c archive.sha256 >/dev/null) || fail "checksum mismatch for $archive"
else
	(cd "$tmp" && shasum -a 256 -c archive.sha256 >/dev/null) || fail "checksum mismatch for $archive"
fi

mkdir "$tmp/release"
tar -xzf "$tmp/$archive" -C "$tmp/release" || fail "could not unpack $archive"
[ -f "$tmp/release/place.sh" ] || fail "$archive has no place.sh"
sh "$tmp/release/place.sh"
