#!/usr/bin/env bash
set -euo pipefail

# Build and release cloudflared for OPNsense
# Runs on freebsd-dev to check for new cloudflared releases, build packages,
# update the local pkg repository, and create GitHub releases.

STATE_FILE="/var/db/cloudflared-build-state"
WORK_DIR="/var/tmp/cloudflared-build"
PKG_REPO_DIR="/var/tmp/cloudflared-repo"
PLUGIN_NAME="os-cloudflared"
PLUGIN_VERSION="1.0"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S %Z')] $*"
}

error() {
    log "ERROR: $*" >&2
    exit 1
}

get_latest_cloudflared_version() {
    curl -s https://api.github.com/repos/cloudflare/cloudflared/releases/latest | \
        grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
}

get_last_built_version() {
    if [[ -f "$STATE_FILE" ]]; then
        cat "$STATE_FILE"
    else
        echo ""
    fi
}

save_built_version() {
    echo "$1" > "$STATE_FILE"
}

build_cloudflared() {
    local version=$1
    log "Building cloudflared $version"
    
    mkdir -p "$WORK_DIR"
    cd "$WORK_DIR"
    
    if [[ -d cloudflared ]]; then
        rm -rf cloudflared
    fi
    
    log "Cloning cloudflared repository at tag $version"
    git clone --depth 1 --branch "$version" https://github.com/cloudflare/cloudflared.git
    
    cd cloudflared
    
    log "Applying FreeBSD patches"
    # Add FreeBSD to build tags
    sed -i "" "s/darwin || linux/darwin || linux || freebsd/" diagnostic/network/collector_unix.go
    sed -i "" "s/darwin || linux/darwin || linux || freebsd/" diagnostic/network/collector_unix_test.go
    
    # Create FreeBSD-specific system collector
    cp diagnostic/system_collector_linux.go diagnostic/system_collector_freebsd.go
    sed -i "" "s/linux/freebsd/" diagnostic/system_collector_freebsd.go
    
    log "Building with Go"
    gmake cloudflared
    
    if [[ ! -f cloudflared ]]; then
        error "Build failed: cloudflared binary not found"
    fi
    
    log "Build complete: $(file cloudflared)"
}

create_plugin_package() {
    local cf_version=$1
    local pkg_name="${PLUGIN_NAME}-${PLUGIN_VERSION}"
    local staging_dir="$WORK_DIR/staging"
    
    log "Creating plugin package $pkg_name"
    
    mkdir -p "$staging_dir"
    cd "$(dirname "$0")/.."
    
    # Copy plugin files
    log "Copying plugin files to staging"
    rsync -av --relative \
        src/opnsense/ \
        +MANIFEST \
        +POST_INSTALL \
        +POST_DEINSTALL \
        "$staging_dir/"
    
    # Install cloudflared binary
    mkdir -p "$staging_dir/usr/local/bin"
    install -m 755 "$WORK_DIR/cloudflared/cloudflared" "$staging_dir/usr/local/bin/"
    
    # Generate manifest with version
    sed "s/{{version}}/$PLUGIN_VERSION/g; s/{{cloudflared_version}}/$cf_version/g" \
        +MANIFEST > "$staging_dir/+MANIFEST"
    
    cd "$staging_dir"
    log "Creating package with pkg create"
    pkg create -m . -r . -p pkg-plist -o "$PKG_REPO_DIR/All/"
    
    local pkg_file="$PKG_REPO_DIR/All/${pkg_name}.pkg"
    if [[ ! -f "$pkg_file" ]]; then
        error "Package creation failed: $pkg_file not found"
    fi
    
    log "Package created: $pkg_file"
}

create_github_release() {
    local version=$1
    local pkg_name="${PLUGIN_NAME}-${PLUGIN_VERSION}"
    local pkg_file="$PKG_REPO_DIR/All/${pkg_name}.pkg"
    local tag="v${PLUGIN_VERSION}-cf${version}"
    
    log "Creating GitHub release for cloudflared $version"
    
    cd "$REPO_DIR"
    
    # Create release with gh CLI
    gh release create "$tag" \
        --title "os-cloudflared ${PLUGIN_VERSION} (cloudflared ${version})" \
        --notes "OPNsense plugin package for cloudflared ${version}" \
        "$pkg_file"
    
    log "GitHub release created: $tag"
}

update_pkg_repository() {
    local cf_version=$1
    local pkg_name="${PLUGIN_NAME}-${PLUGIN_VERSION}"
    local github_url="https://github.com/agoodkind/cloudflared-opnsense/releases/download/v${PLUGIN_VERSION}-cf${cf_version}/${pkg_name}.pkg"
    
    log "Updating pkg repository metadata"
    
    cd "$PKG_REPO_DIR"
    
    # Create metadata that references GitHub
    cat > meta.conf <<EOF
version = 1;
packing_format = "txz";
manifests = "packagesite.yaml";
EOF
    
    # Generate packagesite.yaml with GitHub URL
    cat > packagesite.yaml <<EOF
${pkg_name}:
  name: ${pkg_name}
  version: ${PLUGIN_VERSION}
  origin: opnsense/${pkg_name}
  comment: Cloudflare Tunnel client for OPNsense
  arch: FreeBSD:14:amd64
  www: https://github.com/agoodkind/cloudflared-opnsense
  maintainer: github.com/agoodkind
  prefix: /usr/local
  sum: $(sha256 -q "All/${pkg_name}.pkg")
  flatsize: $(stat -f%z "All/${pkg_name}.pkg")
  path: ${github_url}
EOF
    
    log "Repository metadata updated with GitHub URL"
}

publish_to_cloudflare_pages() {
    log "Publishing repository metadata to Cloudflare Pages"
    
    cd "$REPO_DIR"
    
    # Store current branch
    local current_branch
    current_branch=$(git rev-parse --abbrev-ref HEAD)
    
    # Switch to pkg-repo branch
    if git show-ref --verify --quiet refs/heads/pkg-repo; then
        git checkout pkg-repo
    else
        git checkout --orphan pkg-repo
        git rm -rf . 2>/dev/null || true
    fi
    
    # Copy only metadata files (not the large .pkg)
    mkdir -p repo
    cp "$PKG_REPO_DIR/meta.conf" repo/
    cp "$PKG_REPO_DIR/packagesite.yaml" repo/
    
    # Create index page
    cat > index.html <<EOF
<!DOCTYPE html>
<html>
<head>
    <title>Cloudflared Package Repository</title>
    <style>
        body { font-family: system-ui; max-width: 800px; margin: 50px auto; padding: 20px; }
        code { background: #f4f4f4; padding: 2px 6px; border-radius: 3px; }
        pre { background: #f4f4f4; padding: 15px; border-radius: 5px; overflow-x: auto; }
    </style>
</head>
<body>
    <h1>Cloudflared Package Repository</h1>
    <p>FreeBSD package repository for cloudflared OPNsense plugin.</p>
    
    <h2>Configuration</h2>
    <p>Add to <code>/usr/local/etc/pkg/repos/cloudflared.conf</code>:</p>
    <pre>cloudflared: {
  url: "https://pkg.goodkind.io/repo",
  enabled: yes,
  priority: 10
}</pre>
    
    <h2>Installation</h2>
    <pre>pkg update
pkg install os-cloudflared</pre>
    
    <p><small>Packages hosted on <a href="https://github.com/agoodkind/cloudflared-opnsense/releases">GitHub Releases</a></small></p>
</body>
</html>
EOF
    
    # Commit and push
    git add -A
    if git diff --cached --quiet; then
        log "No changes to publish"
    else
        git commit -m "Update pkg repository metadata - $(date +'%Y-%m-%d %H:%M:%S')"
        git push -u origin pkg-repo
        log "Pushed to pkg-repo branch - Cloudflare Pages will auto-deploy"
    fi
    
    # Return to original branch
    git checkout "$current_branch"
    
    log "Published metadata to Cloudflare Pages"
}

main() {
    log "Checking for new cloudflared releases"
    
    local latest_version
    latest_version=$(get_latest_cloudflared_version)
    log "Latest cloudflared version: $latest_version"
    
    local last_built
    last_built=$(get_last_built_version)
    log "Last built version: ${last_built:-none}"
    
    if [[ "$latest_version" == "$last_built" ]]; then
        log "Already at latest version, nothing to do"
        exit 0
    fi
    
    log "New version detected, starting build"
    build_cloudflared "$latest_version"
    create_plugin_package "$latest_version"
    create_github_release "$latest_version"
    update_pkg_repository "$latest_version"
    publish_to_cloudflare_pages
    save_built_version "$latest_version"
    
    log "Build and release complete"
}

main "$@"
