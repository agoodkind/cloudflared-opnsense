# Go build, lint, and release orchestration for the cloudflared FreeBSD package
# builder, driven by the central go-makefile pipeline (fetched into .make/ at
# parse time). `make help` lists every target; `make check` is the default.
#
# This module is self-contained: it builds, packages, and publishes the
# cloudflared and os-cloudflared FreeBSD packages. The OPNsense plugin packaging
# (Mk/plugins.mk, install, metadata, opnsense-package) lives in the repo-root
# BSD Makefile and is independent of this module. Do NOT add project-local lint,
# vet, fmt, deadcode, or staticcheck targets here; they duplicate the central
# pipeline and let strict rules be bypassed.

# Identity
BINARY := cloudflared-builder
CMD    := .

# Reproducible binary: drop absolute build paths and the VCS stamp so identical
# source produces an identical binary.
GO_BUILD_EXTRA_FLAGS := -trimpath -buildvcs=false

# Pipeline modules. go-build.mk provides the build/install flow plus the strict
# lint and staticcheck gates.
GO_MK_MODULES := go-build.mk

# bootstrap.mk fetches go.mk + golangci.yml + every module in GO_MK_MODULES at
# parse time and -includes them. GO_MK_DEV_DIR (from the environment) points the
# fetch at a local go-makefile checkout when present.
include bootstrap.mk

.DEFAULT_GOAL := check
