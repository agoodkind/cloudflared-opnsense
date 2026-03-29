# Cloudflared OPNsense Plugin

OPNsense plugin for Cloudflare Tunnel (`cloudflared`) with automated FreeBSD package building and distribution.

## Architecture

### Go Binaries

All backend logic is written in Go. There are no Python scripts or shell scripts in the hot path.

| Binary | Where it runs | Purpose |
|---|---|---|
| `cloudflared-configd` | OPNsense router | Reads `config.xml`, writes `rc.conf.d/cloudflared`, writes token or `config.yml`, starts/stops service. Called by configd. |
| `cloudflared-builder` | freebsd-dev build host | Clones cloudflared source, applies FreeBSD patches, builds, packages, creates GitHub release, updates pkg repo metadata. |

### OPNsense Plugin Layout

Follows the canonical OPNsense plugin structure for potential future submission to the community plugins repository.

```text
src/opnsense/
├── mvc/app/
│   ├── controllers/OPNsense/Cloudflared/   # PHP API + UI controllers
│   ├── models/OPNsense/Cloudflared/        # Settings.xml, ACL/ACL.xml, Menu/Menu.xml
│   └── views/OPNsense/Cloudflared/         # Volt templates
├── service/conf/actions.d/                 # configd action definitions
└── scripts/cloudflared/                    # rc.d script
```

### Package Structure

Two packages are built per cloudflared upstream release:

1. **`cloudflared-{version}.pkg`** (~17 MB): upstream binary compiled for FreeBSD amd64
2. **`os-cloudflared-{version}_{revision}.pkg`** (~few KB): OPNsense plugin (UI, configd binary, rc.d)

### Distribution

- Metadata served from freebsd-dev via nginx: `https://cloudflared-opnsense-pkg.goodkind.io`
- Package downloads: GitHub releases with tags like `2026.3.0-freebsd-r1`
- Repository metadata committed to `pkg/` in this repo as a backup

## Prerequisites

### Build host (freebsd-dev)

- FreeBSD 14.3+, Go 1.21+, gmake, git, `gh` CLI authenticated, `pkg` tools, tar with zstd

### Development machine (macOS)

- Go 1.21+

## Building

```bash
# Build Go binaries for local platform (dev/test)
make build

# Cross-compile for FreeBSD amd64 (production)
make freebsd

# Run go vet
make lint

# Format Go source
make fmt
```

On macOS, `/usr/bin/make` is GNU make and does not parse the BSD `.include` used by OPNsense `Mk/plugins.mk`. Use Homebrew `bmake` (`brew install bmake`) and run `bmake build`, or invoke `go build` on the `cmd/` packages directly.

## Build Pipeline

The one-shot pipeline on freebsd-dev:

```bash
# Via cron (every 6 hours) or manually:
./scripts/build-and-release.sh

# Force rebuild even if version unchanged:
./scripts/build-and-release.sh --force

# Subcommands via the Go binary directly:
./dist/cloudflared-builder check           # Is a new version available?
./dist/cloudflared-builder build           # Clone + patch + compile
./dist/cloudflared-builder package         # Create pkg(8) packages
./dist/cloudflared-builder repo            # Regenerate pkg repository index
./dist/cloudflared-builder publish         # GitHub release + push metadata
./dist/cloudflared-builder run             # All of the above
./dist/cloudflared-builder -force run      # Force rebuild with incremented revision
```

## Deploying to OPNsense (Development Iteration)

```bash
# Deploy compiled plugin to a running router (set ROUTER= to your router's IPv6)
make deploy-live ROUTER=3d06:bad:b01::1
```

Staging for packaging uses OPNsense `make install` / `make metadata` with `DESTDIR` (see top-level `Makefile` and `Mk/plugins.mk`); CI runs those via `cloudflared-builder package` on FreeBSD.

## configd binary usage on OPNsense

```bash
# Called automatically by configd on settings save; can also be called manually:
/usr/local/bin/cloudflared-configd reconfigure
/usr/local/bin/cloudflared-configd status
/usr/local/bin/cloudflared-configd version
/usr/local/bin/cloudflared-configd is-enabled  # exits 0 if enabled, 1 otherwise
```

## State Files (on freebsd-dev)

| File | Contents |
|---|---|
| `/var/db/cloudflared-build-state` | Last successfully built cloudflared version |
| `/var/db/cloudflared-revision` | FreeBSD revision number for that version |

## Troubleshooting

**Check build log:**

```bash
ssh root@freebsd-dev "tail -50 /var/log/cloudflared-build.log"
```

**Inspect state:**

```bash
ssh root@freebsd-dev "cat /var/db/cloudflared-build-state /var/db/cloudflared-revision"
```

**Check packages:**

```bash
ssh root@freebsd-dev "ls -lh /var/tmp/cloudflared-repo/All/"
```

**Check repo metadata:**

```bash
curl -s https://cloudflared-opnsense-pkg.goodkind.io/packagesite.yaml | jq .
```

**Verify configd binary works:**

```bash
ssh agoodkind@3d06:bad:b01::1 '/usr/local/bin/cloudflared-configd version'
```
