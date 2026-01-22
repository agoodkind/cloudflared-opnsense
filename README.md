# Cloudflared OPNsense Plugin

[![GitHub Actions Build Status](https://github.com/agoodkind/cloudflared-opnsense/actions/workflows/build-cloudflared.yml/badge.svg)](https://github.com/agoodkind/cloudflared-opnsense/actions)

This repository contains an OPNsense plugin for Cloudflare Tunnel (cloudflared) with automated building and distribution.

## Architecture Overview

### Automated FreeBSD Builds (Current Implementation)
- **Build Host**: `root@freebsd-dev` runs cron job every 30 minutes
- **Version Monitoring**: Checks Cloudflare releases via GitHub API
- **Native Compilation**: Builds cloudflared on FreeBSD with platform-specific patches
- **Deploy Key**: Uses SSH deploy key to push binaries to this repository
- **GitHub Releases**: Automatic creation of releases with downloadable binaries

### OPNsense Plugin
- **MVC Architecture**: Full OPNsense plugin with web interface
- **Configuration Management**: GUI for tunnel configuration and token management
- **Service Integration**: Proper FreeBSD rc.d integration
- **Auto-Updates**: Downloads and installs new versions from GitHub releases

## Installation Summary

**Date**: January 22, 2026
**Build Host**: root@freebsd-dev
**Router Target**: OPNsense routers (pull-based updates)
**Version**: Latest from cloudflared upstream (auto-updated)
**Update Frequency**: 30 minutes (staggered between build and download)

## Components

### 1. Build & Publish System (freebsd-dev)
- **Publish Script**: `cloudflared-publish.sh`
- **Setup Script**: `setup-freebsd-dev.sh`
- **Features**:
  - Monitors GitHub releases for new versions
  - Builds from official Cloudflare source with FreeBSD fixes
  - Publishes to HTTP-accessible directory
  - Generates manifest.json with checksums and metadata
  - State tracking to prevent duplicate builds

### 2. Router Auto-Update System
- **Update Script**: `cloudflared-auto-update.sh`
- **Setup Script**: `setup-router-updates.sh`
- **Features**:
  - Checks manifest for new versions every 30 minutes
  - Downloads and verifies binaries via SHA256
  - Automatic service restart with rollback capability
  - Comprehensive logging and error handling

### 3. Manual Components
- **Token Setup**: `cloudflared-token.sh` - Secure token storage
- **Service Setup**: `cloudflared-rc.sh` - FreeBSD RC script
- **Status Check**: `cloudflared-status.sh` - Service monitoring

### 3. Security Improvements
- **No Plain Text Tokens**: Token must be provided via `CLOUDFLARED_TOKEN` env var
- **Secure Token Setup**: `cloudflared-token.sh` validates and stores securely
- **Environment-Based Config**: All sensitive data via environment variables

## Setup Instructions

### 1. GitHub Deploy Key (REQUIRED FIRST)

**Add this public key to GitHub deploy keys:**

```
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBQXjLxt9dKQ1KBbW6JUzlIEv67/kM4mOb/UTj7SbixL cloudflared-deploy@freebsd-dev
```

**Steps:**
1. Go to: https://github.com/agoodkind/cloudflared-opnsense/settings/keys
2. Click "Add deploy key"
3. Title: `freebsd-dev cloudflared builds`
4. Paste the key above
5. ✅ Check "Allow write access"
6. Click "Add key"

### 2. Automated Build System (Already Configured)

**freebsd-dev is already set up with:**
- ✅ SSH deploy key generated and configured
- ✅ Build script installed at `/usr/local/bin/cloudflared-build-deploy.sh`
- ✅ Cron job running every 30 minutes
- ✅ Log file at `/var/log/cloudflared-build-deploy.log`

**To verify:**
```bash
ssh freebsd-dev 'tail -20 /var/log/cloudflared-build-deploy.log'
ssh freebsd-dev 'crontab -l | grep cloudflared'
```

### 3. OPNsense Router Setup

**As user with sudo on router:**
```bash
# Clone repository
git clone https://github.com/agoodkind/cloudflared-opnsense.git ~/cloudflared-opnsense
cd ~/cloudflared-opnsense

# Set token securely (required once)
export CLOUDFLARED_TOKEN="your-actual-token-here"
./cloudflared-token.sh

# Install RC script
./cloudflared-rc.sh

# Enable service
sudo sysrc cloudflared_enable=YES
sudo service cloudflared start
```

### 4. Download Latest Build

**Once automated builds start (after deploy key is added):**
```bash
# Check GitHub releases for latest version
# Download and install
curl -L -o cloudflared https://github.com/agoodkind/cloudflared-opnsense/releases/latest/download/cloudflared-2026.1.1
chmod +x cloudflared
sudo mv cloudflared /usr/local/bin/
```

```bash
# On router only
export CLOUDFLARED_TOKEN="your-token-here"
cd ~/Sites/configs/router/cloudflared
./cloudflared-token.sh
./cloudflared-rc.sh
./setup-cloudflared-updates.sh  # Deprecated
```

## Management Commands

### Service Control
```bash
# Check status
sudo service cloudflared status

# Restart service
sudo service cloudflared restart

# Stop service
sudo service cloudflared stop

# Start service
sudo service cloudflared start
```

### Monitoring
```bash
# View service logs (on router)
sudo tail -f /var/log/cloudflared.log

# View publish logs (on freebsd-dev)
sudo tail -f /var/log/cloudflared-publish.log

# View update logs (on router)
sudo tail -f /var/log/cloudflared-auto-update.log

# Check version
cloudflared --version

# View metrics
curl http://127.0.0.1:20241/metrics
```

### Manual Operations
```bash
# Trigger manual build/publish (on freebsd-dev)
sudo /usr/local/bin/cloudflared-publish.sh

# Trigger manual update check (on router)
sudo /usr/local/bin/cloudflared-auto-update.sh

# Check published versions (HTTP accessible)
curl http://freebsd-dev.local/cloudflared/manifest.json
```

## Current Status

✅ **Automated Builds**: Running on freebsd-dev (30-minute checks)
✅ **Service**: Running on router (PID varies)
✅ **Connections**: Multiple active to Cloudflare edge locations
✅ **Encryption**: Post-quantum enabled
✅ **Security**: No plain text tokens in repository
✅ **Auto-deploy**: Immediate deployment on new releases

## Troubleshooting

### Service Won't Start (Router)
```bash
# Check rc.conf
grep cloudflared /etc/rc.conf

# Check token file
sudo ls -la /usr/local/etc/cloudflared/token

# Verify token validity
sudo cat /usr/local/etc/cloudflared/token | wc -c  # Should be > 100 chars

# Check logs
sudo tail -20 /var/log/cloudflared.log
```

### Build Issues (freebsd-dev)
```bash
# Check build logs
sudo tail -50 /var/log/cloudflared-auto-build.log

# Check state
sudo cat /var/db/cloudflared-build-state.json

# Manual build test
cd /tmp && sudo CLOUDFLARED_ROUTER="agoodkind@3d06:bad:b01::1" /usr/local/bin/cloudflared-auto-build.sh

# Check Go installation
/usr/local/go/bin/go version
```

### Deployment Issues
```bash
# Test SSH connectivity to router
ssh agoodkind@3d06:bad:b01::1 "echo 'Router reachable'"

# Check router disk space
ssh agoodkind@3d06:bad:b01::1 "df -h /usr/local/bin"

# Verify router service status
ssh agoodkind@3d06:bad:b01::1 "sudo service cloudflared status"
```

### Network Issues
```bash
# Test internet connectivity
curl -I https://github.com

# Test Cloudflare connectivity
curl -I https://cloudflare.com

# Check service status
sudo service cloudflared status
```

## Files in This Directory

### Core Components
- `cloudflared-token.sh` - Secure token setup (requires CLOUDFLARED_TOKEN env var)
- `cloudflared-rc.sh` - FreeBSD RC script installation
- `cloudflared-status.sh` - Service status and monitoring

### Build & Publish System (freebsd-dev)
- `cloudflared-publish.sh` - Automated build and publish script
- `setup-freebsd-dev.sh` - Setup automated publishing on freebsd-dev

### Router Auto-Update System
- `cloudflared-auto-update.sh` - Automatic update checking and installation
- `setup-router-updates.sh` - Setup auto-updates on OPNsense router

### Build Scripts (Reference)
- `cloudflared-build.sh` - Original build script (for reference)

### Documentation
- `README.md` - This documentation

## Build & Update Process

### Publish Phase (freebsd-dev)
1. **Monitoring**: Checks GitHub releases every 30 minutes
2. **Detection**: Identifies new releases via GitHub API
3. **Build**: Clones official repo, applies FreeBSD fixes, builds with Go
4. **Publish**: Copies binary to HTTP-accessible directory
5. **Manifest**: Generates manifest.json with version, checksum, and download URL
6. **State Tracking**: Records successful builds to prevent duplicates

### Update Phase (Router)
1. **Check**: Downloads manifest.json every 30 minutes (offset from publish)
2. **Compare**: Compares latest version against currently installed
3. **Download**: Fetches new binary if update available
4. **Verify**: Validates SHA256 checksum and binary functionality
5. **Install**: Stops service, replaces binary, restarts service
6. **Rollback**: Automatic rollback to backup on failure

### Key Improvements
- **Security**: No tokens stored in plain text
- **Speed**: Builds happen on dedicated FreeBSD system
- **Reliability**: State tracking prevents duplicate builds
- **Monitoring**: Comprehensive logging for debugging

## Migration Notes

### From Legacy to Automated
1. Run `setup-freebsd-dev.sh` on freebsd-dev
2. Remove legacy cron jobs on router
3. Update monitoring scripts to check freebsd-dev logs

### Token Security
- **Old**: Plain text token in script
- **New**: Environment variable `CLOUDFLARED_TOKEN`
- **Never**: Store tokens in version control

## Security Features

- Token validation before storage
- Proper file permissions (600, root:wheel)
- Environment-based credential passing
- No sensitive data in logs
- SSH key-based deployment authentication