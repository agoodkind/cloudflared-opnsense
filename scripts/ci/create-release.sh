#!/usr/bin/env bash
# Create (or recreate) a GitHub release with the built FreeBSD packages.
#
# Required env vars:
#   VERSION      cloudflared version string (e.g. 2026.3.0)
#   REVISION     package revision integer (e.g. 3)
#   ARTIFACT_DIR Path to the downloaded artifact directory
#   GH_TOKEN     GitHub token (set automatically by workflow via github.token)
set -euo pipefail

REPO="agoodkind/cloudflared-opnsense"

TAG="${VERSION}-freebsd-r${REVISION}"
PLUGIN_VER="${VERSION}_${REVISION}"
PKG_CF="${ARTIFACT_DIR}/pkg-repo-build/All/cloudflared-${VERSION}.pkg"
PKG_OS="${ARTIFACT_DIR}/pkg-repo-build/All/os-cloudflared-${PLUGIN_VER}.pkg"
TMP_DIR="$(mktemp -d)"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

package_sha256() {
    local file="$1"
    sha256sum "$file" | awk '{print $1}'
}

package_manifest_sha256() {
    local file="$1"

    tar --zstd --extract --to-stdout --file "$file" "+MANIFEST" \
        | jq -S '
            with_entries(
              select(
                ((.key
                  | gsub("\""; "")
                  | gsub(","; "")
                ) as $normalized_key
                  | $normalized_key != "product_version"
                  and $normalized_key != "version")
                and .key != "files"
              )
            )
            | del(.files["/usr/local/opnsense/version/cloudflared"])
        ' \
        | sha256sum \
        | awk '{print $1}'
}

release_sha256() {
    local release_tag="$1"
    local filename="$2"
    local out_dir="$TMP_DIR"
    local downloaded_file="$out_dir/$filename"

    rm -f "$out_dir"/*.pkg
    if ! gh release download "$release_tag" \
        --repo "${REPO}" \
        --pattern "$filename" \
        --dir "$out_dir" >/dev/null 2>&1; then
        return 1
    fi

    if [[ ! -f "$downloaded_file" ]]; then
        return 1
    fi

    package_sha256 "$downloaded_file"
}

release_manifest_sha256() {
    local release_tag="$1"
    local filename="$2"
    local out_dir="$TMP_DIR"
    local downloaded_file="$out_dir/$filename"

    rm -f "$out_dir"/*.pkg
    if ! gh release download "$release_tag" \
        --repo "${REPO}" \
        --pattern "$filename" \
        --dir "$out_dir" >/dev/null 2>&1; then
        return 1
    fi

    if [[ ! -f "$downloaded_file" ]]; then
        return 1
    fi

    package_manifest_sha256 "$downloaded_file"
}

echo "Target release tag: ${TAG}"

if ! [[ -f "${PKG_CF}" ]]; then
    echo "ERROR: binary package not found: ${PKG_CF}" >&2
    exit 1
fi
if ! [[ -f "${PKG_OS}" ]]; then
    echo "ERROR: plugin package not found: ${PKG_OS}" >&2
    exit 1
fi

PKG_CF_SHA="$(package_sha256 "${PKG_CF}")"
PKG_OS_SHA="$(package_manifest_sha256 "${PKG_OS}")"

# Abort if a higher revision for this version already exists.
# This prevents a slow or retried run from re-publishing a stale revision as the "latest" release.
HIGHEST_EXISTING="$(gh release list \
    --repo "${REPO}" \
    --limit 100 \
    --json tagName \
    --jq ".[] | .tagName | select(startswith(\"${VERSION}-freebsd-r\"))" \
    | sed "s/${VERSION}-freebsd-r//" \
    | sort -n \
    | tail -1)"
HIGHEST_EXISTING="${HIGHEST_EXISTING:-0}"

if [ "${HIGHEST_EXISTING}" -gt "${REVISION}" ]; then
    echo "ERROR: r${HIGHEST_EXISTING} already published for ${VERSION};" \
         "refusing to publish older r${REVISION}." >&2
    exit 1
fi

LATEST_TAG="${VERSION}-freebsd-r${HIGHEST_EXISTING}"
if [ "${HIGHEST_EXISTING}" -gt 0 ]; then
    echo "Latest release for ${VERSION} is ${LATEST_TAG}"

    LATEST_OS="${VERSION}_${HIGHEST_EXISTING}"
    PREV_CF_SHA="$(release_sha256 "${LATEST_TAG}" "cloudflared-${VERSION}.pkg" || true)"
    PREV_OS_SHA="$(release_manifest_sha256 "${LATEST_TAG}" "os-cloudflared-${LATEST_OS}.pkg" || true)"

    if [ -n "${PREV_CF_SHA}" ] && [ -n "${PREV_OS_SHA}" ]; then
        if [ "${PKG_CF_SHA}" = "${PREV_CF_SHA}" ] && \
            [ "${PKG_OS_SHA}" = "${PREV_OS_SHA}" ]; then
            echo "INFO: built packages match latest release hashes; skipping release creation."
            exit 0
        fi
    else
        echo "WARN: could not compare both package hashes from ${LATEST_TAG}; continuing with release."
    fi
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
