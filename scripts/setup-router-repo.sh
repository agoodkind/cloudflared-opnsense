#!/bin/sh
set -eu

# Configure OPNsense router to use custom cloudflared pkg repository
# Run this script on the OPNsense router after freebsd-dev is set up.

REPO_CONF="/usr/local/etc/pkg/repos/cloudflared.conf"
REPO_URL="${CLOUDFLARED_REPO_URL:-https://cloudflared-opnsense-pkg.goodkind.io/pkg}"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S %Z')] $*"
}

error() {
    log "ERROR: $*" >&2
    exit 1
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        error "This script must be run as root"
    fi
}

verify_repo_access() {
    log "Verifying repository access at $REPO_URL"
    
    if ! fetch -qo /dev/null "${REPO_URL}/meta.conf"; then
        error "Cannot access repository at $REPO_URL"
    fi
    
    log "Repository access verified"
}

create_repo_config() {
    log "Creating repository configuration at $REPO_CONF"
    
    mkdir -p "$(dirname "$REPO_CONF")"
    
    cat > "$REPO_CONF" <<EOF
# Custom cloudflared repository
cloudflared: {
  url: "$REPO_URL",
  enabled: yes,
  priority: 10
}
EOF
    
    log "Repository configuration created"
}

update_pkg_database() {
    log "Updating pkg database"
    pkg update -f
    log "Package database updated"
}

install_plugin() {
    log "Installing os-cloudflared plugin"
    
    if pkg info os-cloudflared >/dev/null 2>&1; then
        log "Plugin already installed, upgrading..."
        pkg upgrade -y os-cloudflared
    else
        log "Installing plugin..."
        pkg install -y os-cloudflared
    fi
    
    log "Plugin installation complete"
}

main() {
    check_root
    log "Configuring OPNsense router for cloudflared repository"
    
    verify_repo_access
    create_repo_config
    update_pkg_database
    
    printf "Install os-cloudflared plugin now? [y/N] "
    read -r reply
    case "$reply" in
        [Yy]*)
            install_plugin
            ;;
        *)
            log "Skipping plugin installation"
            log "To install later, run: pkg install os-cloudflared"
            ;;
    esac
    
    log "Setup complete!"
    log ""
    log "Repository configured at: $REPO_CONF"
    log "To upgrade in the future: pkg upgrade os-cloudflared"
}

main "$@"
