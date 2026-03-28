#!/usr/bin/env bash
# Entry point for cron on freebsd-dev.  All logic lives in cloudflared-builder.
#
# Usage:
#   build-and-release.sh [--force]
#
# First call sets up the builder binary if it is not yet compiled.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BUILDER="$REPO_DIR/dist/cloudflared-builder"

if [[ ! -x "$BUILDER" ]]; then
    echo "[$(date +'%Y-%m-%d %H:%M:%S %Z')] cloudflared-builder not found, building..."
    cd "$REPO_DIR"
    gmake freebsd
fi

FLAGS=()
if [[ "${1:-}" == "--force" ]] || [[ "${1:-}" == "-f" ]]; then
    FLAGS+=("-force")
fi

exec "$BUILDER" "${FLAGS[@]}" run
