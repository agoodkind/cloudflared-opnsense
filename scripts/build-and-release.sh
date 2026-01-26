#!/usr/bin/env bash
set -euo pipefail

# Build and release cloudflared for OPNsense
# Runs on freebsd-dev to check for new cloudflared releases, build packages,
# update the local pkg repository, and create GitHub releases.

STATE_FILE="/var/db/cloudflared-build-state"
REVISION_FILE="/var/db/cloudflared-revision"
WORK_DIR="/var/tmp/cloudflared-build"
PKG_REPO_DIR="/var/tmp/cloudflared-repo"
PLUGIN_NAME="os-cloudflared"
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
        grep '"tag_name":' | head -1 | sed -E 's/.*"([^"]+)".*/\1/'
}

get_last_built_version() {
    if [[ -f "$STATE_FILE" ]]; then
        cat "$STATE_FILE"
    else
        echo ""
    fi
}

get_revision_number() {
    local version=$1
    local last_version
    last_version=$(get_last_built_version)
    
    if [[ "$version" == "$last_version" ]] && [[ -f "$REVISION_FILE" ]]; then
        # Same version, increment revision
        local rev
        rev=$(cat "$REVISION_FILE")
        echo $((rev + 1))
    else
        # New version, reset revision
        echo 1
    fi
}

save_built_version() {
    local version=$1
    local revision=$2
    echo "$version" > "$STATE_FILE"
    echo "$revision" > "$REVISION_FILE"
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

create_binary_package() {
    local cf_version=$1
    local staging_dir="$WORK_DIR/binary-staging"

    log "Creating binary package cloudflared-$cf_version"

    mkdir -p "$staging_dir"
    cd "$REPO_DIR"

    # Install cloudflared binary
    mkdir -p "$staging_dir/usr/local/bin"
    install -m 755 "$WORK_DIR/cloudflared/cloudflared" "$staging_dir/usr/local/bin/"

    # Copy package metadata to staging
    cp "packages/cloudflared/+DESC" "$staging_dir/"
    cp "packages/cloudflared/+POST_INSTALL" "$staging_dir/"
    cp "packages/cloudflared/pkg-plist" "$staging_dir/"

    # Generate manifest with version
    sed "s/{{version}}/$cf_version/g" \
        "packages/cloudflared/+MANIFEST" > "$staging_dir/+MANIFEST"

    # Create package
    cd "$staging_dir"
    log "Creating binary package with pkg create"
    pkg create -m . -r . -p pkg-plist -o "$PKG_REPO_DIR/All/"

    local pkg_file="$PKG_REPO_DIR/All/cloudflared-${cf_version}.pkg"
    if [[ ! -f "$pkg_file" ]]; then
        error "Binary package creation failed: $pkg_file not found"
    fi
    
    local size
    size=$(stat -f%z "$pkg_file" 2>/dev/null || stat -c%s "$pkg_file")
    if [[ $size -lt 10000000 ]]; then
        error "Binary package too small ($size bytes), build may have failed"
    fi
    
    log "Binary package created: $pkg_file ($size bytes)"
}

create_plugin_package() {
    local cf_version=$1
    local revision=$2
    local pkg_version="${cf_version}_${revision}"
    local pkg_name="${PLUGIN_NAME}-${pkg_version}"
    local staging_dir="$WORK_DIR/plugin-staging"

    log "Creating plugin package $pkg_name (cloudflared $cf_version, FreeBSD revision $revision)"

    mkdir -p "$staging_dir"
    cd "$REPO_DIR"

    # Copy plugin files to staging
    log "Copying plugin files to staging"

    # Copy OPNsense plugin files
    mkdir -p "$staging_dir/usr/local"
    rsync -av src/opnsense/ "$staging_dir/usr/local/opnsense/"

    # Create required directories
    mkdir -p "$staging_dir/usr/local/etc/cloudflared"
    mkdir -p "$staging_dir/var/log/cloudflared"

    # Install rc.d service script
    mkdir -p "$staging_dir/usr/local/etc/rc.d"
    cp "src/opnsense/scripts/cloudflared/cloudflared.rc" "$staging_dir/usr/local/etc/rc.d/cloudflared"
    chmod 755 "$staging_dir/usr/local/etc/rc.d/cloudflared"

    # Copy package metadata to staging
    cp "packages/os-cloudflared/+DESC" "$staging_dir/"
    cp "packages/os-cloudflared/+POST_INSTALL" "$staging_dir/"
    cp "packages/os-cloudflared/+POST_DEINSTALL" "$staging_dir/"
    cp "packages/os-cloudflared/pkg-plist" "$staging_dir/"

    # Generate manifest with version
    sed "s/{{plugin_version}}/$pkg_version/g; s/{{version}}/$cf_version/g" \
        "packages/os-cloudflared/+MANIFEST" > "$staging_dir/+MANIFEST"

    # Create package
    cd "$staging_dir"
    log "Creating plugin package with pkg create"
    pkg create -m . -r . -p pkg-plist -o "$PKG_REPO_DIR/All/"

    local pkg_file="$PKG_REPO_DIR/All/${pkg_name}.pkg"
    if [[ ! -f "$pkg_file" ]]; then
        error "Plugin package creation failed: $pkg_file not found"
    fi

    log "Plugin package created: $pkg_file"
}

create_github_release() {
    local version=$1
    local revision=$2
    local pkg_version="${version}_${revision}"
    local plugin_pkg_name="${PLUGIN_NAME}-${pkg_version}"
    local binary_pkg_name="cloudflared-${version}"
    local plugin_pkg_file="$PKG_REPO_DIR/All/${plugin_pkg_name}.pkg"
    local binary_pkg_file="$PKG_REPO_DIR/All/${binary_pkg_name}.pkg"
    local tag="${version}-freebsd-r${revision}"

    log "Creating GitHub release for cloudflared $version (revision $revision)"

    cd "$REPO_DIR"

    # Check if release already exists
    if gh release view "$tag" >/dev/null 2>&1; then
        log "Release $tag already exists, deleting and recreating"
        gh release delete "$tag" -y
        git push --delete origin "$tag" 2>/dev/null || true
        git tag -d "$tag" 2>/dev/null || true
    fi

    # Create release with both packages
    gh release create "$tag" \
        --title "Cloudflared ${version} packages for FreeBSD (revision ${revision})" \
        --notes "Cloudflared ${version} packages for FreeBSD\n\n- cloudflared-${version}.pkg: Binary package\n- ${plugin_pkg_name}.pkg: OPNsense plugin package" \
        "$binary_pkg_file" \
        "$plugin_pkg_file"

    log "GitHub release created: $tag"
}

update_pkg_repository() {
    local cf_version=$1
    local revision=$2
    local pkg_version="${cf_version}_${revision}"
    local plugin_pkg_name="${PLUGIN_NAME}-${pkg_version}"
    local binary_pkg_name="cloudflared-${cf_version}"
    local tag="${cf_version}-freebsd-r${revision}"
    local plugin_url="https://github.com/agoodkind/cloudflared-opnsense/releases/download/${tag}/${plugin_pkg_name}.pkg"
    local binary_url="https://github.com/agoodkind/cloudflared-opnsense/releases/download/${tag}/${binary_pkg_name}.pkg"

    log "Updating pkg repository metadata"

    cd "$PKG_REPO_DIR"

    # Clean up old package files so pkg repo only indexes current versions
    # This prevents relative paths for old packages from appearing in packagesite.yaml
    log "Cleaning old packages from repository"
    cd All
    ls -1 *.pkg 2>/dev/null | grep -v "^${plugin_pkg_name}.pkg$" | grep -v "^${binary_pkg_name}.pkg$" | xargs rm -f
    cd ..

    # Use pkg repo to generate proper repository files
    pkg repo .

    # Fix meta.conf to use absolute URLs (remove data field)
    # The data field makes pkg look for packages in /data/ subdirectory
    # We use absolute URLs in packagesite.yaml instead
    sed -i '' 's/^data = .*//' meta.conf

    # pkg repo creates packagesite.pkg containing packagesite.yaml
    # Format: NDJSON (one compact JSON object per line per package)
    # Update package paths to direct GitHub releases download URLs

    # Extract packagesite
    tar -xzf packagesite.pkg

    log "Package URLs - plugin: $plugin_url binary: $binary_url"

    # Update paths in JSON for both packages
    jq -c --arg plugin_url "$plugin_url" --arg binary_url "$binary_url" --arg plugin_ver "$pkg_version" --arg binary_ver "$cf_version" '
        if .name == "os-cloudflared" and .version == $plugin_ver then
            .path = $plugin_url | .repopath = $plugin_url
        elif .name == "cloudflared" and .version == $binary_ver then
            .path = $binary_url | .repopath = $binary_url
        else
            .
        end
    ' packagesite.yaml > packagesite.tmp
    mv packagesite.tmp packagesite.yaml

    # Recompress with zstd (pkg repo uses zstd, not gzip)
    rm -f packagesite.pkg
    tar --zstd -cf packagesite.pkg packagesite.yaml

    log "Repository metadata updated"
}

publish_to_cloudflare_pages() {
    log "Publishing repository metadata to Cloudflare Pages"
    
    cd "$REPO_DIR"
    
    # Create pkg directory in main branch
    rm -rf pkg
    mkdir -p pkg
    
    # Copy metadata files (packagesite.pkg is already zstd-compressed)
    cp "$PKG_REPO_DIR/meta.conf" pkg/
    cp "$PKG_REPO_DIR/packagesite.yaml" pkg/
    cp "$PKG_REPO_DIR/packagesite.pkg" pkg/
    
    # Create _headers file for Cloudflare Pages to fix pkg Last-Modified warning
    local build_date
    build_date=$(date -u "+%a, %d %b %Y %H:%M:%S GMT")
    cat > pkg/_headers << EOF
/*
  Last-Modified: ${build_date}
EOF
    
    # Commit and push
    git add pkg/
    if git diff --cached --quiet; then
        log "No changes to publish"
    else
        git commit -m "Update pkg repository"
        git push origin main
        log "Pushed to main - Cloudflare Pages will auto-deploy"
    fi
    
    log "Published metadata to Cloudflare Pages"
}

main() {
    local force=false
    if [[ "${1:-}" == "--force" ]] || [[ "${1:-}" == "-f" ]]; then
        force=true
        log "Force rebuild requested"
    fi
    
    log "Updating repository"
    cd "$REPO_DIR"
    git fetch origin main
    git reset --hard origin/main
    
    mkdir -p "$PKG_REPO_DIR/All"
    
    log "Checking for new cloudflared releases"
    
    local latest_version
    latest_version=$(get_latest_cloudflared_version)
    log "Latest cloudflared version: $latest_version"
    
    local last_built
    last_built=$(get_last_built_version)
    log "Last built version: ${last_built:-none}"
    
    if [[ "$latest_version" == "$last_built" ]] && [[ "$force" == "false" ]]; then
        log "Already at latest version, nothing to do"
        log "Use --force to rebuild with incremented revision"
        exit 0
    fi
    
    log "New version detected, starting build"
    
    local revision
    revision=$(get_revision_number "$latest_version")
    log "Building revision $revision"
    
    build_cloudflared "$latest_version"
    create_binary_package "$latest_version"
    create_plugin_package "$latest_version" "$revision"
    create_github_release "$latest_version" "$revision"
    update_pkg_repository "$latest_version" "$revision"
    publish_to_cloudflare_pages
    save_built_version "$latest_version" "$revision"
    
    log "Build and release complete (${latest_version}_${revision})"
}

main "$@"
