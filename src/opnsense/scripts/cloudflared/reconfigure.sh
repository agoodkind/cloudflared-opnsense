#!/bin/sh
# Reconfigure cloudflared based on OPNsense settings
# - Updates rc.conf settings via sysrc
# - Writes token file or config.yml based on mode
# - Starts/stops/restarts service as needed

set -e

CONFIG_DIR="/usr/local/etc/cloudflared"
TOKEN_FILE="${CONFIG_DIR}/token"
CONFIG_FILE="${CONFIG_DIR}/config.yml"
GENERATE_SCRIPT="/usr/local/opnsense/scripts/cloudflared/generate_config.py"

# Ensure config directory exists
mkdir -p "${CONFIG_DIR}"
chmod 700 "${CONFIG_DIR}"

# Generate config and get settings from OPNsense
config_json=$("${GENERATE_SCRIPT}" --json 2>/dev/null || echo '{}')

# Parse settings from JSON output
enabled=$(echo "${config_json}" | /usr/local/bin/jq -r '.enabled // false')
mode=$(echo "${config_json}" | /usr/local/bin/jq -r '.mode // "token"')
token=$(echo "${config_json}" | /usr/local/bin/jq -r '.token // ""')
post_quantum=$(echo "${config_json}" | /usr/local/bin/jq -r '.post_quantum // true')
edge_ip_version=$(echo "${config_json}" | /usr/local/bin/jq -r '.edge_ip_version // "auto"')
protocol=$(echo "${config_json}" | /usr/local/bin/jq -r '.protocol // "auto"')
loglevel=$(echo "${config_json}" | /usr/local/bin/jq -r '.loglevel // "info"')

# Update rc.conf settings
if [ "${enabled}" = "true" ]; then
    sysrc cloudflared_enable="YES" >/dev/null 2>&1
else
    sysrc cloudflared_enable="NO" >/dev/null 2>&1
fi

sysrc cloudflared_mode="${mode}" >/dev/null 2>&1
sysrc cloudflared_token_file="${TOKEN_FILE}" >/dev/null 2>&1
sysrc cloudflared_config="${CONFIG_FILE}" >/dev/null 2>&1
sysrc cloudflared_loglevel="${loglevel}" >/dev/null 2>&1

# Post-quantum setting
if [ "${post_quantum}" = "true" ]; then
    sysrc cloudflared_post_quantum="YES" >/dev/null 2>&1
else
    sysrc cloudflared_post_quantum="NO" >/dev/null 2>&1
fi

# Edge IP version
case "${edge_ip_version}" in
    ipv4|4) sysrc cloudflared_edge_ip_version="4" >/dev/null 2>&1 ;;
    ipv6|6) sysrc cloudflared_edge_ip_version="6" >/dev/null 2>&1 ;;
    *)      sysrc cloudflared_edge_ip_version="" >/dev/null 2>&1 ;;
esac

# Protocol
case "${protocol}" in
    quic|http2) sysrc cloudflared_protocol="${protocol}" >/dev/null 2>&1 ;;
    *)          sysrc cloudflared_protocol="" >/dev/null 2>&1 ;;
esac

# Handle mode-specific configuration
case "${mode}" in
    token)
        # Write token to file
        if [ -n "${token}" ]; then
            echo "${token}" > "${TOKEN_FILE}"
            chmod 600 "${TOKEN_FILE}"
        fi
        ;;
    config)
        # Generate config.yml for locally-managed mode
        "${GENERATE_SCRIPT}" --config > "${CONFIG_FILE}"
        chmod 600 "${CONFIG_FILE}"
        ;;
esac

# Start, stop, or restart service based on enabled state
if [ "${enabled}" = "true" ]; then
    if /usr/local/etc/rc.d/cloudflared status >/dev/null 2>&1; then
        /usr/local/etc/rc.d/cloudflared restart
    else
        /usr/local/etc/rc.d/cloudflared start
    fi
else
    /usr/local/etc/rc.d/cloudflared stop 2>/dev/null || true
fi

echo "Cloudflared reconfigured successfully"