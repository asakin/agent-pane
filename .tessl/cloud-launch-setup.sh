#!/usr/bin/env bash
# Tessl cloud-run setup for agent-pane. Runs from the clone root before every
# cloud run. Only what a run needs to build and test: Go module cache and the
# extension's npm dependencies. Tools: the machine's Go honours GOTOOLCHAIN,
# so go.mod's go 1.27.3 fetches its own toolchain over the machine's 1.27.2;
# the machine's Node 24 covers extension/ (no engines constraint).
set -eo pipefail

echo "==> Go: resolving the toolchain go.mod asks for"
export GOTOOLCHAIN=auto
go version
go mod download

echo "==> Extension: installing npm dependencies"
cd extension
npm ci
cd ..

echo "==> Setup complete"
