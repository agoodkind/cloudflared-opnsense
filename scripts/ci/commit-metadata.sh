#!/usr/bin/env bash
# Commit updated pkg repository metadata back to main.
#
# Required env vars (set by workflow):
#   ARTIFACT_DIR  Path to the downloaded artifact directory
#
# When running on a non-main branch (e.g. a feature branch for testing)
# the metadata commit is skipped to avoid a non-fast-forward push to main.
set -euo pipefail

git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git config user.name "github-actions[bot]"

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [ "${CURRENT_BRANCH}" != "main" ]; then
    echo "Running on branch '${CURRENT_BRANCH}', not 'main' — skipping metadata push."
    exit 0
fi

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
