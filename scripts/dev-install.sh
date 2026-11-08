#!/bin/sh
# Builds Agent Pane from this checkout and installs it the way a release is
# installed: it lays the build out like a release archive and runs
# scripts/place.sh on it. Nothing of Agent Pane is downloaded; npm fetches the
# extension's dependencies.
#
# The binary reports version "dev". To go back to a release, run the install
# command from the README.
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$root"

go build -o bin/agent-pane .
npm --prefix extension ci
npm --prefix extension run build

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp bin/agent-pane scripts/place.sh README.md LICENSE "$tmp/"
mkdir -p "$tmp/extensions" "$tmp/skills"
cp -R extension/dist/chrome-mv3 "$tmp/extensions/chrome"
cp -R agents "$tmp/agents"
cp -R skills/agent-pane "$tmp/skills/agent-pane"

sh "$tmp/place.sh"
