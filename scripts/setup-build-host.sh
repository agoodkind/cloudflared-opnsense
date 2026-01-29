#!/usr/bin/env bash
set -euo pipefail

# One-time setup for freebsd-dev as a cloudflared build host
# Installs dependencies and sets up cron job for automated builds.

CRON_SCRIPT="/usr/local/bin/cloudflared-build-cron"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S %Z')] $*"
}

error() {
    log "ERROR: $*" >&2
    exit 1
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "This script must be run as root"
    fi
}

install_dependencies() {
    log "Installing build dependencies"
    pkg install -y git go gmake rsync curl gh
}

setup_repository_backup() {
    log "Repository metadata backup via git"
    
    cd "$REPO_DIR"
    
    # pkg/ directory is committed to main branch for backup/versioning
    # Actual serving is done from freebsd-dev nginx at /var/tmp/cloudflared-repo/
    
    log "Metadata served from freebsd-dev (nginx port 8080)"
}

setup_cron() {
    log "Setting up cron job for automated builds"
    
    cat > "$CRON_SCRIPT" <<EOF
#!/usr/bin/env bash
# Run cloudflared build and release check
cd "$REPO_DIR" || exit 1
./scripts/build-and-release.sh >> /var/log/cloudflared-build.log 2>&1
EOF
    
    chmod +x "$CRON_SCRIPT"
    
    # Add cron job (daily at 2 AM)
    local cron_line="0 2 * * * $CRON_SCRIPT"
    
    if crontab -l 2>/dev/null | grep -Fq "$CRON_SCRIPT"; then
        log "Cron job already exists"
    else
        (crontab -l 2>/dev/null || true; echo "$cron_line") | crontab -
        log "Cron job added: $cron_line"
    fi
}

verify_git_config() {
    log "Verifying git configuration"
    
    cd "$REPO_DIR"
    
    if ! git config user.email >/dev/null 2>&1; then
        log "Configuring git user"
        git config user.email "cloudflared-build@freebsd-dev"
        git config user.name "Cloudflared Build"
    fi
    
    log "Git configured"
}

setup_gh_auth() {
    log "Checking GitHub CLI authentication"
    
    if gh auth status >/dev/null 2>&1; then
        log "GitHub CLI already authenticated"
        return 0
    fi
    
    log "WARNING: GitHub CLI not authenticated"
    log "GitHub releases will be skipped until authenticated"
    log ""
    log "To authenticate, run manually:"
    log "  gh auth login"
    log ""
    log "Or use a token:"
    log "  echo 'your_token' | gh auth login --with-token"
}

main() {
    check_root
    log "Starting freebsd-dev setup for cloudflared builds"
    
    install_dependencies
    verify_git_config
    setup_gh_auth
    setup_repository_backup
    setup_cron
    
    log "Setup complete!"
    log ""
    log "Architecture:"
    log "- GitHub Releases: Hosts .pkg files (via gh CLI)"
    log "- freebsd-dev nginx: Serves pkg repository metadata (port 8080)"
    log "- Domain routing: Cloudflare DNS → Traefik → nginx"
    log "- pkg downloads packages from GitHub via metadata URLs"
    log ""
    log "Next steps:"
    log "1. Run initial build: $REPO_DIR/scripts/build-and-release.sh"
    log "2. Configure routers with: scripts/setup-router-repo.sh"
    log "3. Builds will run automatically daily at 2 AM"
}

main "$@"
