# Cloudflared OPNsense Plugin

OPNsense plugin for Cloudflare Tunnel (cloudflared) with automated FreeBSD package building and distribution.

## Architecture

### Build System
- **Build Host**: freebsd-dev (FreeBSD 14.3) with native Go toolchain
- **Build Script**: `scripts/build-and-release.sh` - automated package creation
- **Execution**: Cron job checks for new cloudflared releases every 30 minutes
- **Version Detection**: GitHub API monitoring for upstream cloudflare/cloudflared releases

### Package Structure

Two packages are built for each cloudflared release:

1. **cloudflared-{version}.pkg** - Binary package (~17MB)
   - Cloudflared binary compiled for FreeBSD
   - Installed to `/usr/local/bin/cloudflared`
   - Independent of OPNsense plugin

2. **os-cloudflared-{version}_{revision}.pkg** - OPNsense plugin package (~6KB)
   - Web UI (MVC controllers, models, views)
   - Configuration management
   - Service integration scripts
   - Requires cloudflared binary package

### Distribution

**GitHub Releases**: Packages uploaded to GitHub releases with tags like `2026.1.1-freebsd-r1`

**FreeBSD pkg Repository**:
- Metadata served from freebsd-dev (nginx port 8080): `https://cloudflared-opnsense-pkg.goodkind.io`
- Domain routing: Cloudflare DNS → Traefik → nginx on freebsd-dev
- Repository files: `meta.conf`, `data.pkg`, `packagesite.yaml`, `packagesite.pkg`
- Package downloads: From GitHub releases

## Build Process

### Version Check
```bash
# Script checks GitHub API for latest cloudflared release
latest=$(curl -s https://api.github.com/repos/cloudflare/cloudflared/releases/latest | grep tag_name)
```

### Build Flow

1. **Update Repository**
   ```bash
   git fetch origin main
   git reset --hard origin/main
   ```

2. **Clone cloudflared Source**
   ```bash
   git clone --depth 1 --branch $version https://github.com/cloudflare/cloudflared.git
   ```

3. **Apply FreeBSD Patches**
   - Add FreeBSD to build tags in `diagnostic/network/collector_unix.go`
   - Create FreeBSD-specific system collector
   - Enable FreeBSD support in diagnostics

4. **Build Binary**
   ```bash
   gmake cloudflared  # Uses Go vendor modules
   ```

5. **Create Binary Package**
   - Stage binary to `/usr/local/bin/cloudflared`
   - Copy package metadata (`+MANIFEST`, `+DESC`, `+POST_INSTALL`, `pkg-plist`)
   - Generate manifest with version substitution
   - Run `pkg create` to build `cloudflared-{version}.pkg`
   - Verify package size (must be > 10MB)

6. **Create Plugin Package**
   - Copy OPNsense plugin files from `src/opnsense/`
   - Install rc.d service script
   - Create required directories (`/usr/local/etc/cloudflared`, `/var/log/cloudflared`)
   - Generate manifest with plugin version `{cloudflared_version}_{freebsd_revision}`
   - Run `pkg create` to build `os-cloudflared-{version}_{revision}.pkg`

7. **Upload to GitHub Releases**
   ```bash
   gh release create $tag \
       --title "Cloudflared $version packages for FreeBSD (revision $revision)" \
       cloudflared-$version.pkg \
       os-cloudflared-${version}_${revision}.pkg
   ```

8. **Update pkg Repository Metadata**
   - Clean old packages from `/var/tmp/cloudflared-repo/All/`
   - Run `pkg repo .` to generate repository files
   - Remove `data` field from `meta.conf` (enables absolute URLs)
   - Update `packagesite.yaml` with package download URLs
   - Compress metadata with zstd

9. **Publish Repository Metadata**
   - Copy `meta.conf`, `meta`, `data.pkg`, `packagesite.yaml`, `packagesite.pkg` to `pkg/`
   - Commit and push to main branch (for backup/versioning)
   - Metadata served directly from freebsd-dev nginx

### Revision Tracking

- State file: `/var/db/cloudflared-build-state` (current cloudflared version)
- Revision file: `/var/db/cloudflared-revision` (FreeBSD-specific revision number)
- Same cloudflared version gets incremented FreeBSD revision on rebuild

## Package Repository

### Structure
```
/var/tmp/cloudflared-repo/
├── All/
│   ├── cloudflared-2026.1.1.pkg
│   └── os-cloudflared-2026.1.1_20.pkg
├── meta.conf          # Repository configuration
├── meta               # Repository metadata
├── data.pkg           # Package data archive
├── packagesite.yaml   # Package manifest (NDJSON)
└── packagesite.pkg    # Compressed package manifest (zstd)
```

### meta.conf Format
```
version = 2;
packing_format = "tzst";
manifests = "packagesite.yaml";
filesite = "filesite.yaml";
manifests_archive = "packagesite";
filesite_archive = "filesite";
```

Note: `data` field removed to support absolute URLs in packagesite.yaml

### packagesite.yaml Format

NDJSON (one compact JSON object per line per package):
```json
{"name":"os-cloudflared","version":"2026.1.1_20","path":"http://[...]/os-cloudflared-2026.1.1_20.pkg",...}
{"name":"cloudflared","version":"2026.1.1","path":"http://[...]/cloudflared-2026.1.1.pkg",...}
```

## Manual Build

```bash
# SSH to freebsd-dev
ssh root@freebsd-dev

# Run build script
cd /root/cloudflared-opnsense
./scripts/build-and-release.sh --force

# Check build artifacts
ls -lh /var/tmp/cloudflared-repo/All/

# View repository metadata
cat /var/tmp/cloudflared-repo/packagesite.yaml
```

## Files

### Build Scripts
- `scripts/build-and-release.sh` - Main build and release automation
- `scripts/setup-build-host.sh` - Initial freebsd-dev setup
- `scripts/setup-router-repo.sh` - OPNsense repository configuration

### Setup Scripts
- `setup-freebsd-dev.sh` - Configure freebsd-dev for automated builds
- `setup-router-updates.sh` - Configure OPNsense to use package repository

### Package Metadata
- `packages/cloudflared/` - Binary package metadata (+MANIFEST, +DESC, +POST_INSTALL, pkg-plist)
- `packages/os-cloudflared/` - Plugin package metadata (+MANIFEST, +DESC, +POST_INSTALL, +POST_DEINSTALL, pkg-plist)

### OPNsense Plugin Source
- `src/opnsense/mvc/` - MVC components (controllers, models, views)
- `src/opnsense/scripts/cloudflared/` - Backend scripts (config generation, rc.d service)
- `src/opnsense/service/conf/actions.d/` - configd actions
- `src/opnsense/www/menu/` - Menu integration

### Repository Files
- `pkg/` - Repository metadata backup (meta.conf, data.pkg, packagesite.*)
- Served from `/var/tmp/cloudflared-repo/` on freebsd-dev

## Build Requirements

- FreeBSD 14.3 or later
- Go 1.21+ (`gmake` uses vendored modules)
- Git
- jq (JSON processing)
- GitHub CLI (`gh`) with authentication
- pkg tools (`pkg create`, `pkg repo`)
- tar with zstd support

## Troubleshooting

### Build Failures

Check build logs:
```bash
ssh root@freebsd-dev "tail -50 /var/log/cloudflared-build.log"
```

Verify state files:
```bash
ssh root@freebsd-dev "cat /var/db/cloudflared-build-state /var/db/cloudflared-revision"
```

### Package Issues

Verify package creation:
```bash
ssh root@freebsd-dev "ls -lh /var/tmp/cloudflared-repo/All/"
ssh root@freebsd-dev "pkg info -f /var/tmp/cloudflared-repo/All/os-cloudflared-*.pkg"
```

Check repository metadata:
```bash
curl -s https://cloudflared-opnsense-pkg.goodkind.io/packagesite.yaml | jq .
```
