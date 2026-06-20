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

- Go 1.26.4+ (matching `go.mod`)
- For BSD-make targets locally: `brew install bmake` (macOS `/usr/bin/make` is GNU make and does not parse `Mk/plugins.mk`)

### CI runtime

GitHub Actions provides everything else. The publish job requires `CF_ACCOUNT_ID`
and `CLOUDFLARE_API_TOKEN` secrets for Cloudflare R2 uploads. No persistent build
host is required.

## Building

The OPNsense plugin and the `cloudflared-configd` runtime binary live in the repo-root module under the BSD `Makefile` (`Mk/plugins.mk`, `install`, `metadata`, `opnsense-package`). The build/package/publish orchestrator is a separate Go module, `goodkind.io/cloudflared-builder`, under `builder/`, driven by the central go-makefile pipeline.

### Local development

```bash
# Plugin runtime (configd) for the host, reproducible flags. Root module, BSD make.
make build

# Cross-compile configd for FreeBSD amd64.
make freebsd

# Orchestrator module: full lint + test gate (go-makefile), and host build.
cd builder && make check
```

### CI build pipeline

`.github/workflows/build.yml` runs on:

- Cron every 6 hours (polls upstream for new cloudflared releases)
- Pushes to `main` (test + build, publish only when package content changed)
- `workflow_dispatch` (manual trigger)
- Every PR (test + build, no publish)

Stages:

1. **`check`** (Ubuntu): runs `cloudflared-builder plan`, which queries the GitHub API for the latest cloudflared release and derives the next FreeBSD revision from existing release tags. No persistent state.
2. **`test`** (Ubuntu): root-module `go vet` and `go test -race`, cross-compile `cloudflared-configd` for FreeBSD, and cross-compile the builder for FreeBSD.
3. **`builder`** (reusable go-makefile CI): runs the canonical `builder/` go-makefile quality and build gates through `agoodkind/go-makefile/.github/workflows/_ci.yml@main`.
4. **`build`** (Ubuntu host with `vmactions/freebsd-vm@v1.4.6` running FreeBSD 14.2): builds `cloudflared-configd` (root module) and `cloudflared-builder` (its module) with `-trimpath -buildvcs=false`, then runs the builder's `build`, `package`, and `repo` subcommands to produce pkg(8) packages.
5. **`publish`** (Ubuntu, skipped on PRs): runs `cloudflared-builder publish`, which compares each package's normalized `+MANIFEST` content against the latest release for the same upstream version and, only when the binary or plugin package content changed, uploads packages and metadata to R2 through Cloudflare's Go SDK and cuts a tagged GitHub release.

### Meaningful content change

Publish is gated per package on a content fingerprint that is robust to build nondeterminism. Each package's `+MANIFEST` is normalized by dropping version- and revision-identity fields (`version`, `annotations.product_version`, `annotations.product_hash`, and the `/usr/local/opnsense/version/cloudflared` stamp) and hashed. A new release is cut when there is no prior release for the upstream version, or when the binary or plugin fingerprint differs from the latest release. Reproducible builds (`-trimpath -buildvcs=false`, plus the cloudflared build pinned to the upstream commit date) make identical source produce identical installed files, so a fingerprint change reflects a real content change rather than rebuild noise.

### Builder binary subcommands

```bash
cd builder && go build -trimpath -buildvcs=false -o ../dist/cloudflared-builder .

./dist/cloudflared-builder plan              # Print version + next revision.
./dist/cloudflared-builder build             # Clone + patch + compile.
./dist/cloudflared-builder package           # Create pkg(8) packages.
./dist/cloudflared-builder repo              # Regenerate pkg repository index.
./dist/cloudflared-builder -check-only publish # Emit the publish decision only.
./dist/cloudflared-builder publish           # Publish to R2 + GitHub release if content changed.
./dist/cloudflared-builder run               # check → build → package → repo → publish.
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
cd builder && go build -trimpath -buildvcs=false -o ../dist/cloudflared-builder .
cd .. && ./dist/cloudflared-builder -version <version> -revision <rev> build
```
