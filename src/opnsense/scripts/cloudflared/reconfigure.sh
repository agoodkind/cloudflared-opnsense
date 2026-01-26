#!/bin/sh
# Generate config and restart cloudflared if enabled

set -e

CONFIG_DIR="/usr/local/etc/cloudflared"
CONFIG_FILE="$CONFIG_DIR/config.yml"

# Generate config from OPNsense settings
/usr/local/opnsense/scripts/cloudflared/generate_config.py > "$CONFIG_FILE"

# Check if enabled and restart
if /usr/local/opnsense/scripts/cloudflared/is_enabled.py; then
    /usr/local/etc/rc.d/cloudflared restart
fi