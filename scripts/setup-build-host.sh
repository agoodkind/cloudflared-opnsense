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

setup_cloudflare_pages() {
    log "Setting up Cloudflare Pages branch"
    
    cd "$REPO_DIR"
    
    # Create pkg-repo branch if it doesn't exist
    if ! git show-ref --verify --quiet refs/heads/pkg-repo; then
        log "Creating pkg-repo branch"
        git checkout --orphan pkg-repo
        git rm -rf . 2>/dev/null || true
        
        # Create placeholder
        cat > README.txt <<'EOF'
FreeBSD Package Repository for cloudflared-opnsense

This branch is automatically updated by the build system.
Served via Cloudflare Pages at pkg.goodkind.io
EOF
        
        git add README.txt
        git commit -m "Initialize pkg-repo branch"
        git push -u origin pkg-repo
        git checkout main
    fi
    
    log "pkg-repo branch ready"
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
    log "Setting up GitHub CLI authentication"
    
    if gh auth status >/dev/null 2>&1; then
        log "GitHub CLI already authenticated"
        return 0
    fi
    
    log ""
    log "GitHub CLI needs authentication for creating releases"
    log "Run this command and follow the prompts:"
    log "  gh auth login"
    log ""
    log "Choose:"
    log "  - GitHub.com"
    log "  - HTTPS"
    log "  - Authenticate with: Login with a web browser"
    log ""
    read -p "Press Enter to continue with authentication..."
    
    gh auth login
    
    if gh auth status >/dev/null 2>&1; then
        log "GitHub CLI authenticated successfully"
    else
        error "GitHub CLI authentication failed"
    fi
}

main() {
    check_root
    log "Starting freebsd-dev setup for cloudflared builds"
    
    install_dependencies
    verify_git_config
    setup_gh_auth
    setup_cloudflare_pages
    setup_cron
    
    log "Setup complete!"
    log ""
    log "Architecture:"
    log "- GitHub Releases: Hosts .pkg files (via gh CLI)"
    log "- Cloudflare Pages: Serves pkg repository metadata"
    log "- pkg downloads packages from GitHub via metadata URLs"
    log ""
    log "Next steps:"
    log "1. Connect repo to Cloudflare Pages:"
    log "   - Go to Workers & Pages > Create application > Pages"
    log "   - Connect to Git > Select cloudflared-opnsense repo"
    log "   - Production branch: pkg-repo"
    log "   - Build settings: None (pre-built static files)"
    log "   - Add custom domain: pkg.goodkind.io"
    log "2. Run initial build: $REPO_DIR/scripts/build-and-release.sh"
    log "3. Configure routers with: scripts/setup-router-repo.sh"
    log "4. Builds will run automatically daily at 2 AM"
}

main "$@"
