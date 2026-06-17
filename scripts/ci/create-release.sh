#!/usr/bin/env bash
# Create or skip a GitHub release with the built FreeBSD packages.
#
# Required env vars:
#   VERSION      cloudflared version string (for example 2026.3.0)
#   REVISION     package revision integer (for example 3)
#   ARTIFACT_DIR Path to the downloaded artifact directory
#   GH_TOKEN     GitHub token (set automatically by workflow via github.token)
set -euo pipefail

REPO="agoodkind/cloudflared-opnsense"
MODE="publish"

if [[ "${1:-}" == "--check-only" ]]; then
    MODE="check"
    shift
fi

TAG="${VERSION}-freebsd-r${REVISION}"
PLUGIN_VER="${VERSION}_${REVISION}"
PKG_CF="${ARTIFACT_DIR}/pkg-repo-build/All/cloudflared-${VERSION}.pkg"
PKG_OS="${ARTIFACT_DIR}/pkg-repo-build/All/os-cloudflared-${PLUGIN_VER}.pkg"
TMP_DIR="$(mktemp -d)"
SHOULD_PUBLISH="false"
PUBLISH_REASON=""
LATEST_TAG=""

cleanup() {
    rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

write_output() {
    local key="$1"
    local value="$2"

    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
        printf '%s=%s\n' "${key}" "${value}" >> "${GITHUB_OUTPUT}"
    fi
}

package_sha256() {
    local file_path="$1"

    sha256sum "${file_path}" | awk '{print $1}'
}

package_manifest_sha256() {
    local file_path="$1"

    tar --zstd --extract --to-stdout --file "${file_path}" "+MANIFEST" \
        | jq -S '
            del(
                .version,
                .flatsize,
                .annotations.product_version,
                .annotations.product_hash,
                .files["/usr/local/opnsense/version/cloudflared"]
            )
        ' \
        | sha256sum \
        | awk '{print $1}'
}

highest_existing_revision() {
    local releases_json

    releases_json="$(gh release list --repo "${REPO}" --limit 100 --json tagName)"
    RELEASES_JSON="${releases_json}" VERSION_PREFIX="${VERSION}-freebsd-r" python3 - <<'PY'
import json
import os

releases = json.loads(os.environ["RELEASES_JSON"])
prefix = os.environ["VERSION_PREFIX"]
revisions = []

for release in releases:
    tag_name = release.get("tagName", "")
    if not tag_name.startswith(prefix):
        continue
    suffix = tag_name[len(prefix):]
    try:
        revisions.append(int(suffix))
    except ValueError:
        continue

if revisions:
    print(max(revisions))
PY
}

download_release_asset() {
    local release_tag="$1"
    local filename="$2"
    local output_dir="$3"

    rm -f "${output_dir}"/*.pkg
    if ! gh release download "${release_tag}" \
        --repo "${REPO}" \
        --pattern "${filename}" \
        --dir "${output_dir}" >/dev/null 2>&1; then
        return 1
    fi

    if [[ ! -f "${output_dir}/${filename}" ]]; then
        return 1
    fi
}

release_sha256() {
    local release_tag="$1"
    local filename="$2"
    local output_dir="${TMP_DIR}"

    download_release_asset "${release_tag}" "${filename}" "${output_dir}" || return 1
    package_sha256 "${output_dir}/${filename}"
}

release_manifest_sha256() {
    local release_tag="$1"
    local filename="$2"
    local output_dir="${TMP_DIR}"

    download_release_asset "${release_tag}" "${filename}" "${output_dir}" || return 1
    package_manifest_sha256 "${output_dir}/${filename}"
}

decide_publish() {
    local highest_existing
    local latest_os
    local pkg_cf_sha
    local pkg_os_sha
    local prev_cf_sha
    local prev_os_sha

    echo "Target release tag: ${TAG}"

    if [[ ! -f "${PKG_CF}" ]]; then
        echo "ERROR: binary package not found: ${PKG_CF}" >&2
        return 1
    fi
    if [[ ! -f "${PKG_OS}" ]]; then
        echo "ERROR: plugin package not found: ${PKG_OS}" >&2
        return 1
    fi

    pkg_cf_sha="$(package_sha256 "${PKG_CF}")"
    pkg_os_sha="$(package_manifest_sha256 "${PKG_OS}")"

    highest_existing="$(highest_existing_revision)"
    highest_existing="${highest_existing:-0}"

    if [[ "${highest_existing}" -gt "${REVISION}" ]]; then
        echo "ERROR: r${highest_existing} already published for ${VERSION}; refusing to publish older r${REVISION}." >&2
        return 1
    fi

    if [[ "${highest_existing}" -eq 0 ]]; then
        SHOULD_PUBLISH="true"
        PUBLISH_REASON="no_prior_release_for_version"
        return 0
    fi

    LATEST_TAG="${VERSION}-freebsd-r${highest_existing}"
    latest_os="${VERSION}_${highest_existing}"
    echo "Latest release for ${VERSION} is ${LATEST_TAG}"

    if ! prev_cf_sha="$(release_sha256 "${LATEST_TAG}" "cloudflared-${VERSION}.pkg")"; then
        echo "ERROR: could not download or hash cloudflared-${VERSION}.pkg from ${LATEST_TAG}." >&2
        return 1
    fi

    if ! prev_os_sha="$(release_manifest_sha256 "${LATEST_TAG}" "os-cloudflared-${latest_os}.pkg")"; then
        echo "ERROR: could not download or hash os-cloudflared-${latest_os}.pkg from ${LATEST_TAG}." >&2
        return 1
    fi

    if [[ "${pkg_cf_sha}" == "${prev_cf_sha}" && "${pkg_os_sha}" == "${prev_os_sha}" ]]; then
        SHOULD_PUBLISH="false"
        PUBLISH_REASON="no_meaningful_package_change"
        return 0
    fi

    SHOULD_PUBLISH="true"
    PUBLISH_REASON="package_content_changed"
}

decide_publish

write_output "should_publish" "${SHOULD_PUBLISH}"
write_output "publish_reason" "${PUBLISH_REASON}"
write_output "target_tag" "${TAG}"
write_output "latest_tag" "${LATEST_TAG}"

echo "Publish decision: ${SHOULD_PUBLISH} (${PUBLISH_REASON})"

if [[ "${MODE}" == "check" ]]; then
    exit 0
fi

if [[ "${SHOULD_PUBLISH}" != "true" ]]; then
    echo "Skipping release creation because package content did not change."
    exit 0
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
