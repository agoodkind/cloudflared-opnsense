#!/usr/bin/env bash
set -euo pipefail

# Setup git hooks for cloudflared-opnsense
# Configures git to use the hooks in .githooks/ directory.

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_DIR"

echo "Configuring git hooks path..."
git config core.hooksPath .githooks

echo "Git hooks configured to use .githooks/ directory."
echo "Active hooks:"
ls -l .githooks/
