#!/bin/sh
# Cloudflared auto-update script for OPNsense
# Checks for new versions and automatically updates from published builds
#
# This script runs on the router and:
# 1. Checks manifest for new versions
# 2. Downloads and verifies new builds
# 3. Updates cloudflared with rollback capability
#
# Configuration:
# - CLOUDFLARED_MANIFEST_URL: URL to manifest.json (default: http://freebsd-dev.local/cloudflared/manifest.json)

set -e

# Configuration
MANIFEST_URL="${CLOUDFLARED_MANIFEST_URL:-http://freebsd-dev.local/cloudflared/manifest.json}"
DOWNLOAD_DIR="/tmp/cloudflared-update"
LOG_FILE="/var/log/cloudflared-auto-update.log"
BACKUP_SUFFIX=".backup.$(date +%Y%m%d_%H%M%S)"

# Log function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $*" | tee -a "$LOG_FILE"
}

# Get current installed version
get_current_version() {
    /usr/local/bin/cloudflared --version 2>/dev/null | head -1 | awk '{print $3}' || echo "unknown"
}

# Check manifest for latest version
check_manifest() {
    log "Checking manifest: $MANIFEST_URL"

    if ! curl -s --max-time 10 "$MANIFEST_URL" > /tmp/manifest.json; then
        log "Failed to download manifest"
        return 1
    fi

    # Parse manifest
    if ! jq -e '.latest_version' /tmp/manifest.json > /dev/null 2>&1; then
        log "Invalid manifest format"
        return 1
    fi

    local latest_version
    local download_url
    local checksum

    latest_version=$(jq -r '.latest_version' /tmp/manifest.json)
    download_url=$(jq -r '.download_url' /tmp/manifest.json)
    checksum=$(jq -r '.checksum' /tmp/manifest.json)

    echo "$latest_version|$download_url|$checksum"
}

# Download and verify build
download_and_verify() {
    local version="$1"
    local download_url="$2"
    local expected_checksum="$3"

    log "Downloading $version from $download_url"

    # Create download directory
    rm -rf "$DOWNLOAD_DIR"
    mkdir -p "$DOWNLOAD_DIR"
    cd "$DOWNLOAD_DIR"

    # Download binary
    if ! curl -s --max-time 30 -o "cloudflared-new" "$download_url"; then
        log "Download failed"
        return 1
    fi

    # Verify checksum
    local actual_checksum
    actual_checksum=$(sha256sum cloudflared-new | awk '{print $1}')

    if [ "$actual_checksum" != "$expected_checksum" ]; then
        log "Checksum verification failed. Expected: $expected_checksum, Got: $actual_checksum"
        return 1
    fi

    # Verify binary works
    if ! chmod +x cloudflared-new || ! ./cloudflared-new --version >/dev/null 2>&1; then
        log "Binary verification failed"
        return 1
    fi

    # Verify version matches
    local binary_version
    binary_version=$(./cloudflared-new --version | head -1 | awk '{print $3}')

    if [ "$binary_version" != "$version" ]; then
        log "Version mismatch. Expected: $version, Got: $binary_version"
        return 1
    fi

    log "Download and verification successful"
    echo "$DOWNLOAD_DIR/cloudflared-new"
}

# Update cloudflared
update_cloudflared() {
    local new_binary="$1"
    local version="$2"

    log "Updating to version $version"

    # Stop service
    log "Stopping cloudflared service"
    service cloudflared stop

    # Create backup
    local backup_path="/usr/local/bin/cloudflared$BACKUP_SUFFIX"
    log "Creating backup: $backup_path"
    cp /usr/local/bin/cloudflared "$backup_path"

    # Install new binary
    log "Installing new binary"
    mv "$new_binary" /usr/local/bin/cloudflared
    chmod +x /usr/local/bin/cloudflared

    # Test new binary
    log "Testing new binary"
    if /usr/local/bin/cloudflared --version >/dev/null 2>&1; then
        log "Binary test successful, starting service"
        service cloudflared start

        # Wait a moment and check status
        sleep 2
        if service cloudflared status >/dev/null 2>&1; then
            log "Update successful"
            return 0
        else
            log "Service failed to start"
        fi
    else
        log "Binary test failed"
    fi

    # Rollback on failure
    log "Update failed, rolling back"
    mv "$backup_path" /usr/local/bin/cloudflared
    service cloudflared start
    return 1
}

# Main logic
main() {
    log "Starting cloudflared auto-update check"

    # Get current version
    local current_version
    current_version=$(get_current_version)
    log "Current version: $current_version"

    # Check for updates
    local manifest_data
    if ! manifest_data=$(check_manifest); then
        log "Manifest check failed"
        exit 1
    fi

    local latest_version download_url checksum
    latest_version=$(echo "$manifest_data" | cut -d'|' -f1)
    download_url=$(echo "$manifest_data" | cut -d'|' -f2)
    checksum=$(echo "$manifest_data" | cut -d'|' -f3)

    log "Latest available: $latest_version"

    # Check if update needed
    if [ "$current_version" = "$latest_version" ]; then
        log "Already at latest version"
        exit 0
    fi

    # Download and update
    if new_binary=$(download_and_verify "$latest_version" "$download_url" "$checksum"); then
        if update_cloudflared "$new_binary" "$latest_version"; then
            log "Auto-update completed successfully"
        else
            log "Auto-update failed"
            exit 1
        fi
    else
        log "Download/verification failed"
        exit 1
    fi
}

# Run main
main "$@"