#!/bin/bash
# Builds web/dist and fails if the build did not really produce it: on old Node
# versions Vite aborts with exit code 0.
set -e
cd "$(dirname "$0")/.."

if [ ! -f web/package.json ]; then
    echo "web/package.json not found, skipping web build"
    exit 0
fi

required=$(cat .nvmrc)
current=$(node -p 'process.versions.node.split(".")[0]')
if [ "$current" -lt "$required" ]; then
    echo "Node $current is too old for the web build, $required or newer is required. Run: nvm use" >&2
    exit 1
fi

started=$(mktemp)
trap 'rm -f "$started"' EXIT
(cd web && npm install --silent && npm run build --silent)
if [ ! web/dist/index.html -nt "$started" ]; then
    echo "Web build did not update web/dist/index.html" >&2
    exit 1
fi
