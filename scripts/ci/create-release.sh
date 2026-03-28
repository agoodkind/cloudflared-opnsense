#!/usr/bin/env bash
# Create (or recreate) a GitHub release with the built FreeBSD packages.
#
# Required env vars:
#   VERSION      cloudflared version string (e.g. 2026.3.0)
#   REVISION     package revision integer (e.g. 3)
#   ARTIFACT_DIR Path to the downloaded artifact directory
#   GH_TOKEN     GitHub token (set automatically by workflow via github.token)
set -euo pipefail

TAG="${VERSION}-freebsd-r${REVISION}"
PLUGIN_VER="${VERSION}_${REVISION}"
PKG_CF="${ARTIFACT_DIR}/pkg-repo-build/All/cloudflared-${VERSION}.pkg"
PKG_OS="${ARTIFACT_DIR}/pkg-repo-build/All/os-cloudflared-${PLUGIN_VER}.pkg"

echo "Target release tag: ${TAG}"

if ! [[ -f "${PKG_CF}" ]]; then
    echo "ERROR: binary package not found: ${PKG_CF}" >&2
    exit 1
fi
if ! [[ -f "${PKG_OS}" ]]; then
    echo "ERROR: plugin package not found: ${PKG_OS}" >&2
    exit 1
fi

if gh release view "${TAG}" &>/dev/null; then
    echo "Deleting existing release ${TAG}..."
    gh release delete "${TAG}" --yes --cleanup-tag
fi

echo "Creating release ${TAG}..."
gh release create "${TAG}" \
    --title "Cloudflared ${VERSION} FreeBSD (r${REVISION})" \
    --notes "FreeBSD amd64 packages for cloudflared ${VERSION}." \
    "${PKG_CF}" "${PKG_OS}"

echo "Release ${TAG} created."
