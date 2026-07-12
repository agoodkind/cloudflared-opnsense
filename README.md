# Cloudflared for OPNsense

This repository builds the OPNsense plugin and FreeBSD packages that run
Cloudflare Tunnel through `cloudflared`.

## Configure the tunnel

Install the published plugin package from the package repository configured for
your OPNsense environment, then open the Cloudflared settings page. Saving the
settings asks configd to apply the configuration and start, restart, or stop the
service.

Choose one tunnel mode:

- **Token** uses a Cloudflare-managed tunnel token.
- **Config File** writes a local `config.yml` and tunnel credentials. It requires
  an account tag, tunnel ID, and tunnel secret.

Config File mode also manages ingress rules. Each enabled rule maps a hostname
to an HTTP, HTTPS, TCP, SSH, or RDP service. The generated configuration ends
with an HTTP 404 rule for requests that match no configured hostname.

Use the following commands on the router when you need to inspect the installed
service:

```sh
/usr/local/bin/cloudflared-configd reconfigure
/usr/local/bin/cloudflared-configd status
/usr/local/bin/cloudflared-configd version
/usr/local/bin/cloudflared-configd is-enabled
```

`is-enabled` exits with status 0 when the plugin is enabled and status 1 when
it is disabled.

## Build locally

The repository has two Go modules. The root module builds the router helper,
and `builder/` packages and publishes the FreeBSD artifacts.

Install the Go version declared by each module. On macOS, install BSD Make with
`brew install bmake`, because the root Makefile uses BSD Make syntax.

```sh
# Build the router helper for the current host.
bmake build

# Cross-compile the router helper for FreeBSD amd64.
bmake freebsd

# Run the builder module checks. This uses GNU Make.
make -C builder check
```

`bmake deploy-live ROUTER=<router>` copies a development build and the plugin
files to the selected router, reloads its templates, and restarts configd. Use
it only for development because it changes the live router outside package
management.

## Build and publish packages

GitHub Actions checks for new upstream `cloudflared` releases every six hours,
on pushes to `main`, and when manually dispatched. Pull requests run the build
and a temporary R2 upload, readback, and cleanup when repository credentials
are available. Pull requests never create releases or publish production
objects.

The workflow verifies both Go modules, builds the FreeBSD packages in its
configured VM, and generates package repository metadata. On non-pull-request
runs, it compares each newly built package with the latest release for the same
upstream version. It publishes to Cloudflare R2 and creates a GitHub release
only when a package is new or its installed content changed.

Each release contains `cloudflared-<version>.pkg` and
`os-cloudflared-<version>_<revision>.pkg`.

The publish job requires `CF_ACCOUNT_ID` and `CLOUDFLARE_API_TOKEN`. GitHub
releases use tags in the form `<cloudflared-version>-freebsd-r<revision>`.

## Troubleshoot delivery

Inspect recent workflow runs and releases before rebuilding or republishing:

```sh
gh run list --repo agoodkind/cloudflared-opnsense --limit 5
gh run view <run-id> --log
gh release list --repo agoodkind/cloudflared-opnsense --limit 5
```

The package repository URL belongs to the deployment environment and is not
stored in this repository. Check its configured URL there before querying
`packagesite.yaml` or changing router package settings.

## Repository layout

- `cmd/cloudflared-configd` applies the OPNsense settings and manages the
  `cloudflared` service.
- `src/opnsense` contains the plugin UI, settings model, configd actions, and
  rc.d integration.
- `builder` builds `cloudflared`, creates the two FreeBSD packages, generates
  repository metadata, and publishes release artifacts.
- `.github/workflows/build.yml` defines the build, preview, and publish flow.
