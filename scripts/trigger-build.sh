#!/usr/bin/env bash
set -euo pipefail

# Trigger cloudflared build on freebsd-dev via SSH
# This script is used by git hooks or manually to start the build pipeline.

BUILD_HOST="${CLOUDFLARED_BUILD_HOST:-freebsd-dev}"
BUILD_USER="${CLOUDFLARED_BUILD_USER:-root}"
BUILD_SCRIPT="${CLOUDFLARED_BUILD_SCRIPT:-/root/cloudflared-opnsense/scripts/build-and-release.sh}"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S %Z')] $*"
}

log "Triggering build on ${BUILD_HOST}..."

# Trigger build asynchronously to avoid blocking the caller
# We use --force to ensure a build starts even if the cloudflared version hasn't changed
# (e.g. when plugin source code has changed)
ssh -o ConnectTimeout=5 "${BUILD_USER}@${BUILD_HOST}" \
    "nohup ${BUILD_SCRIPT} --force > /var/log/cloudflared-build-trigger.log 2>&1 &"

log "Build trigger sent to ${BUILD_HOST}"
log "Check logs on ${BUILD_HOST}: tail -f /var/log/cloudflared-build.log"
