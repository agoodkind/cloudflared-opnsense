#!/bin/sh
# Setup automated cloudflared publishing on freebsd-dev
# This script configures freebsd-dev to automatically build and publish
# cloudflared builds when new upstream releases are available
#
# Run this on freebsd-dev as root

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_FILE="/var/log/cloudflared-setup.log"

# Log function
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $*" | tee -a "$LOG_FILE"
}

log "Starting freebsd-dev setup for automated cloudflared publishing"

# Check if running on FreeBSD
if [ "$(uname)" != "FreeBSD" ]; then
    log "Error: This script must be run on FreeBSD"
    exit 1
fi

# Check for required tools
for cmd in git go jq curl; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        log "Error: $cmd is required but not installed"
        exit 1
    fi
done

# Setup publish directory (configurable)
PUBLISH_DIR="${CLOUDFLARED_PUBLISH_DIR:-/var/www/cloudflared}"
log "Setting up publish directory: $PUBLISH_DIR"
sudo mkdir -p "$PUBLISH_DIR"
sudo chown www:www "$PUBLISH_DIR" 2>/dev/null || sudo chown root:wheel "$PUBLISH_DIR"

log "Installing publish script"
sudo cp "$SCRIPT_DIR/cloudflared-publish.sh" /usr/local/bin/
sudo chmod +x /usr/local/bin/cloudflared-publish.sh

# Create log directory
sudo mkdir -p /var/log
sudo touch /var/log/cloudflared-publish.log
sudo chmod 644 /var/log/cloudflared-publish.log

# Setup cron job (every 30 minutes)
CRON_JOB="*/30 * * * * root /usr/local/bin/cloudflared-publish.sh >> /var/log/cloudflared-publish.log 2>&1"
CRON_FILE="/etc/cron.d/cloudflared-publish"

log "Installing cron job"
echo "$CRON_JOB" | sudo tee "$CRON_FILE" > /dev/null
sudo chmod 644 "$CRON_FILE"

# Test the setup
log "Testing publish script"
/usr/local/bin/cloudflared-publish.sh || log "Initial test failed (expected on first run)"

log "Setup complete!"
log ""
log "Configuration:"
log "1. Publish directory: $PUBLISH_DIR"
log "2. Base URL: ${CLOUDFLARED_BASE_URL:-http://freebsd-dev.local/cloudflared}"
log ""
log "Next steps:"
log "1. Configure web server to serve $PUBLISH_DIR at the base URL"
log "2. On router, set CLOUDFLARED_MANIFEST_URL to manifest.json URL"
log "3. Test manually: sudo /usr/local/bin/cloudflared-publish.sh"
log "4. Monitor logs: tail -f /var/log/cloudflared-publish.log"
log ""
log "The system will now automatically check for new cloudflared releases every 30 minutes"
log "and publish them for router download."