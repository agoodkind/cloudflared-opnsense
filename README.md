# Cloudflared OPNsense Plugin

OPNsense plugin for Cloudflare Tunnel (`cloudflared`) with automated FreeBSD package building and distribution via GitHub Actions and Cloudflare R2.

## Architecture

### Go Binaries

All backend logic is written in Go. There are no Python scripts or shell scripts in the hot path.

| Binary | Where it runs | Purpose |
|---|---|---|
| `cloudflared-configd` | OPNsense router (FreeBSD amd64) | Reads `config.xml`, writes `rc.conf.d/cloudflared`, writes token or `config.yml`, starts/stops service. Called by configd. |
| `cloudflared-builder` | GitHub Actions (FreeBSD VM via `vmactions/freebsd-vm`) | Clones cloudflared source, applies FreeBSD patches, builds, packages, and writes pkg repo metadata. |

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

Packages and pkg repository metadata are uploaded to **Cloudflare R2** by CI. Tagged GitHub releases provide a secondary download channel.

| Channel | Purpose |
|---|---|
| Cloudflare R2 bucket | Primary pkg repository served to routers (referenced in `/usr/local/etc/pkg/repos/`). |
| GitHub releases | Tagged like `2026.3.0-freebsd-r1`. Contains the same artifacts as a backup and audit trail. |

The legacy `cloudflared-opnsense-pkg.goodkind.io` nginx host on `freebsd-dev` is no longer in use.

## Prerequisites

### Development machine (macOS or Linux)

- Go 1.21+ (matching `go.mod`)
- For BSD-make targets locally: `brew install bmake` (macOS `/usr/bin/make` is GNU make and does not parse `Mk/plugins.mk`)

### CI runtime

GitHub Actions provides everything else. No persistent build host is required.

## Building

### Local development

```bash
# Build Go binaries for the host platform (dev/test).
make build

# Cross-compile both binaries for FreeBSD amd64.
# This is what CI uses as the test gate before paying for the FreeBSD VM step.
make freebsd

# Lint and format.
make lint
make fmt
```

### CI build pipeline

`.github/workflows/build.yml` runs on:

- Cron every 6 hours (polls upstream for new cloudflared releases)
- `workflow_dispatch` (manual trigger)
- Every PR (test + build, no publish)

Stages:

1. **`check`** (Ubuntu): Queries the GitHub API for the latest cloudflared release. Derives the next FreeBSD revision number from existing release tags. No persistent state.
2. **`test`** (Ubuntu): `go vet`, `go test -race`, cross-compile for FreeBSD as a build gate, shellcheck on CI scripts.
3. **`build`** (Ubuntu host with `vmactions/freebsd-vm@v1.4.3` running FreeBSD 14.2): Builds `cloudflared-builder` and `cloudflared-configd` natively in the FreeBSD VM. Runs the builder's `build`, `package`, and `repo` subcommands to produce pkg(8) packages.
4. **`publish`** (Ubuntu, skipped on PRs): Uploads packages and metadata to R2 via `scripts/ci/upload-r2.sh`. Cuts a tagged GitHub release via `scripts/ci/create-release.sh`.

### Builder binary subcommands (run inside the FreeBSD VM in CI)

```bash
./dist/cloudflared-builder check      # Is a new version available?
./dist/cloudflared-builder build      # Clone + patch + compile.
./dist/cloudflared-builder package    # Create pkg(8) packages.
./dist/cloudflared-builder repo       # Regenerate pkg repository index.
./dist/cloudflared-builder run        # All of the above.
./dist/cloudflared-builder -force run # Force rebuild with incremented revision.
```

These can also be invoked locally on a FreeBSD host for debugging, but the canonical path is the GHA workflow.

## Deploying to OPNsense (Development Iteration)

For pushing a freshly-built binary to a running router during development:

```bash
make deploy-live ROUTER=3d06:bad:b01::1
```

This calls `make freebsd` first, then scps the binary, rc.d script, and MVC tree to the router.

For production deployment, install the published pkg from the R2 repository via OPNsense's plugin manager.

## configd binary usage on OPNsense

```bash
# Called automatically by configd on settings save. Can also be called manually:
/usr/local/bin/cloudflared-configd reconfigure
/usr/local/bin/cloudflared-configd status
/usr/local/bin/cloudflared-configd version
/usr/local/bin/cloudflared-configd is-enabled  # exits 0 if enabled, 1 otherwise
```

## Troubleshooting

**Check most recent CI run:**

```bash
gh run list --repo agoodkind/cloudflared-opnsense --limit 5
gh run view <run-id> --log
```

**Check published packages:**

```bash
gh release list --repo agoodkind/cloudflared-opnsense --limit 5
```

**Check R2 repo metadata:**

```bash
# Replace with the actual R2 public URL once configured.
curl -s https://<r2-public-host>/packagesite.yaml | jq .
```

**Verify configd binary on a router:**

```bash
ssh agoodkind@3d06:bad:b01::1 '/usr/local/bin/cloudflared-configd version'
```

**Reproduce the FreeBSD build locally without CI:**

```bash
# On a FreeBSD 14.x host with go + gmake + git:
go build -o dist/cloudflared-builder ./cmd/cloudflared-builder
./dist/cloudflared-builder -version <version> -revision <rev> build
```
