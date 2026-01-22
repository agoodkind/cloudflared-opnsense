#!/bin/sh
# Cloudflared publish script for freebsd-dev
# Builds cloudflared and publishes to public download location
#
# This script runs on freebsd-dev and:
# 1. Builds latest cloudflared from source
# 2. Publishes to a public HTTP location for router download
# 3. Updates version manifest for auto-update checking
#
# Configuration:
# - CLOUDFLARED_PUBLISH_DIR: Local directory to publish builds (default: /var/www/cloudflared)
# - CLOUDFLARED_BASE_URL: Base URL where builds are served (default: http://freebsd-dev.local/cloudflared)

set -e

# Configuration
PUBLISH_DIR="${CLOUDFLARED_PUBLISH_DIR:-/var/www/cloudflared}"
BASE_URL="${CLOUDFLARED_BASE_URL:-http://freebsd-dev.local/cloudflared}"
BUILD_DIR="/tmp/cloudflared-build"
LOG_FILE="/var/log/cloudflared-publish.log"
MANIFEST_FILE="$PUBLISH_DIR/manifest.json"

# Log function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $*" | tee -a "$LOG_FILE"
}

# Get latest release info
get_latest_release() {
    curl -s "https://api.github.com/repos/cloudflare/cloudflared/releases/latest" | \
        jq -r '.tag_name // empty'
}

# Get current published version
get_published_version() {
    if [ -f "$MANIFEST_FILE" ]; then
        jq -r '.latest_version // empty' "$MANIFEST_FILE" 2>/dev/null || echo ""
    else
        echo ""
    fi
}

# Update manifest
update_manifest() {
    local version="$1"
    local filename="$2"
    local checksum="$3"
    local build_time="$4"

    mkdir -p "$PUBLISH_DIR"
    jq -n \
        --arg version "$version" \
        --arg filename "$filename" \
        --arg checksum "$checksum" \
        --arg build_time "$build_time" \
        --arg url "$BASE_URL/$filename" \
        '{
            latest_version: $version,
            filename: $filename,
            checksum: $checksum,
            build_time: $build_time,
            download_url: $url
        }' > "$MANIFEST_FILE"

    log "Updated manifest: $MANIFEST_FILE"
}

# Build cloudflared
build_cloudflared() {
    local version="$1"
    log "Building cloudflared $version"

    # Clean up
    rm -rf "$BUILD_DIR"
    mkdir -p "$BUILD_DIR"
    cd "$BUILD_DIR"

    # Clone and checkout specific version
    log "Cloning repository"
    git clone --depth 1 --branch "$version" "https://github.com/cloudflare/cloudflared.git" .

    # Fix build constraints for FreeBSD
    log "Applying FreeBSD build fixes"
    sed -i "" "s/darwin || linux/darwin || linux || freebsd/" diagnostic/network/collector_unix.go
    sed -i "" "s/darwin || linux/darwin || linux || freebsd/" diagnostic/network/collector_unix_test.go

    # Copy FreeBSD system collector
    cp diagnostic/system_collector_linux.go diagnostic/system_collector_freebsd.go
    sed -i "" "s/linux/freebsd/" diagnostic/system_collector_freebsd.go

    # Build
    log "Building cloudflared"
    export PATH="/usr/local/go/bin:$PATH"
    go build -o "cloudflared-$version" "./cmd/cloudflared"

    # Verify
    if [ ! -f "cloudflared-$version" ] || ! "./cloudflared-$version" --version >/dev/null 2>&1; then
        log "Build verification failed"
        return 1
    fi

    log "Build successful: $BUILD_DIR/cloudflared-$version"
    echo "$BUILD_DIR/cloudflared-$version"
}

# Publish build
publish_build() {
    local binary_path="$1"
    local version="$2"

    local filename="cloudflared-$version"
    local publish_path="$PUBLISH_DIR/$filename"

    log "Publishing $version to $publish_path"

    # Create publish directory
    mkdir -p "$PUBLISH_DIR"

    # Copy binary
    cp "$binary_path" "$publish_path"
    chmod 644 "$publish_path"

    # Generate checksum
    local checksum
    checksum=$(sha256sum "$publish_path" | awk '{print $1}')

    # Update manifest
    update_manifest "$version" "$filename" "$checksum" "$(date -Iseconds)"

    log "Published successfully. Download URL: $BASE_URL/$filename"
}

# Main logic
main() {
    log "Starting cloudflared publish check"

    # Get versions
    latest_version=$(get_latest_release)
    published_version=$(get_published_version)

    if [ -z "$latest_version" ]; then
        log "Failed to get latest release info"
        exit 1
    fi

    log "Latest upstream: $latest_version"
    log "Currently published: ${published_version:-none}"

    # Check if we need to build
    if [ "$latest_version" = "$published_version" ]; then
        log "Already published latest version"
        exit 0
    fi

    # Build and publish
    if binary_path=$(build_cloudflared "$latest_version"); then
        publish_build "$binary_path" "$latest_version"
        log "Publish cycle completed successfully"
    else
        log "Build failed"
        exit 1
    fi
}

# Run main
main "$@"