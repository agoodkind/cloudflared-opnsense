#!/usr/bin/env bash
# Commit updated pkg repository metadata back to main.
#
# Required env vars (set by workflow):
#   ARTIFACT_DIR  Path to the downloaded artifact directory
set -euo pipefail

git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git config user.name "github-actions[bot]"

rm -rf pkg
mkdir -p pkg
cp -a "${ARTIFACT_DIR}/pkg/." pkg/

git add pkg/
if git diff --staged --quiet; then
    echo "No pkg metadata changes to commit."
    exit 0
fi

git commit -m "Update pkg repository metadata"
git push origin HEAD:main
echo "Metadata pushed to main."
