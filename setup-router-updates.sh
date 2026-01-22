#!/bin/sh
# Setup cloudflared auto-update on OPNsense router
# This script configures the router to automatically check for and install
# new cloudflared versions from the published builds
#
# Run this on the router as root

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_FILE="/var/log/cloudflared-router-setup.log"

# Log function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $*" | tee -a "$LOG_FILE"
}

log "Starting router setup for automated cloudflared updates"

# Check if running on FreeBSD (OPNsense)
if [ "$(uname)" != "FreeBSD" ]; then
    log "Error: This script must be run on FreeBSD/OPNsense"
    exit 1
fi

# Check for required tools
for cmd in curl jq; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log "Installing missing dependency: $cmd"
        pkg install -y "$cmd"
    fi
done

log "Installing auto-update script"
cp "$SCRIPT_DIR/cloudflared-auto-update.sh" /usr/local/bin/
chmod +x /usr/local/bin/cloudflared-auto-update.sh

# Create log directory
mkdir -p /var/log
touch /var/log/cloudflared-auto-update.log
chmod 644 /var/log/cloudflared-auto-update.log

# Setup cron job (every 30 minutes, offset by 15 minutes from publisher)
CRON_JOB="15,45 * * * * root /usr/local/bin/cloudflared-auto-update.sh >> /var/log/cloudflared-auto-update.log 2>&1"
CRON_FILE="/etc/cron.d/cloudflared-auto-update"

log "Installing cron job"
echo "$CRON_JOB" | tee "$CRON_FILE" > /dev/null
chmod 644 "$CRON_FILE"

# Test the setup (if manifest URL is configured)
if [ -n "$CLOUDFLARED_MANIFEST_URL" ]; then
    log "Testing auto-update script"
    /usr/local/bin/cloudflared-auto-update.sh || log "Initial test failed (expected if no updates available)"
else
    log "Skipping test - CLOUDFLARED_MANIFEST_URL not set"
fi

log "Setup complete!"
log ""
log "Configuration:"
log "1. Manifest URL: ${CLOUDFLARED_MANIFEST_URL:-not set}"
log "2. Update schedule: Every 30 minutes (15 and 45 past hour)"
log ""
log "Next steps:"
log "1. Set CLOUDFLARED_MANIFEST_URL environment variable:"
log "   echo 'CLOUDFLARED_MANIFEST_URL=http://freebsd-dev.local/cloudflared/manifest.json' >> /etc/crontab-env"
log "2. Test manually: /usr/local/bin/cloudflared-auto-update.sh"
log "3. Monitor logs: tail -f /var/log/cloudflared-auto-update.log"
log ""
log "The router will now automatically check for new cloudflared versions every 30 minutes"
log "and update itself when new versions are available."