# Makefile for os-cloudflared OPNsense plugin
#
# OPNsense plugin build system integration.
# Follows the layout described in:
#   https://github.com/opnsense/plugins/blob/master/Mk/plugins.mk
#
# Usage:
#   make                  Build Go binaries for FreeBSD amd64
#   make package          Create pkg(8) packages (runs on freebsd-dev)
#   make install          Deploy plugin files to a running OPNsense (dev only)
#   make clean            Remove build artifacts
#   make lint             Run go vet on all packages
#   make fmt              Run gofmt on all Go source

PLUGIN_NAME    = os-cloudflared
PLUGIN_VERSION ?= $(shell cat pkg/version 2>/dev/null || echo "dev")

GO       = go
GOOS_BSD = freebsd
GOARCH   = amd64

BUILD_DIR = dist
CMD_CONFIGD = cmd/cloudflared-configd
CMD_BUILDER = cmd/cloudflared-builder

CONFIGD_BIN = $(BUILD_DIR)/cloudflared-configd
BUILDER_BIN = $(BUILD_DIR)/cloudflared-builder

# OPNsense plugin deployment paths (used by `make install` on a live router)
PREFIX     = /usr/local
SCRIPTS    = $(PREFIX)/opnsense/scripts/cloudflared
SERVICE    = $(PREFIX)/opnsense/service/conf/actions.d
MVC        = $(PREFIX)/opnsense/mvc/app
WWW        = $(PREFIX)/opnsense/www

.PHONY: all build freebsd clean lint fmt install package

all: build

## Build Go binaries for the current platform (dev/test use).
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -o $(CONFIGD_BIN) ./$(CMD_CONFIGD)
	$(GO) build -o $(BUILDER_BIN) ./$(CMD_BUILDER)

## Cross-compile Go binaries for FreeBSD amd64 (production).
freebsd:
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS_BSD) GOARCH=$(GOARCH) \
		$(GO) build -o $(CONFIGD_BIN) ./$(CMD_CONFIGD)
	GOOS=$(GOOS_BSD) GOARCH=$(GOARCH) \
		$(GO) build -o $(BUILDER_BIN) ./$(CMD_BUILDER)
	@echo "FreeBSD binaries written to $(BUILD_DIR)/"

## Create FreeBSD packages.  Must run on the freebsd-dev build host.
package: freebsd
	$(BUILDER_BIN) run

## Install plugin to a running OPNsense (for development iteration).
## Requires SSH access; set ROUTER= to the router's address.
ROUTER ?= 3d06:bad:b01::1

install: freebsd
	ssh root@$(ROUTER) "mkdir -p $(SCRIPTS) $(PREFIX)/bin $(PREFIX)/etc/rc.d"
	scp $(CONFIGD_BIN) root@$(ROUTER):$(PREFIX)/bin/cloudflared-configd
	scp src/opnsense/scripts/cloudflared/cloudflared.rc \
		root@$(ROUTER):$(PREFIX)/etc/rc.d/cloudflared
	scp -r src/opnsense/mvc/app/controllers/OPNsense \
		root@$(ROUTER):$(MVC)/controllers/
	scp -r src/opnsense/mvc/app/models/OPNsense \
		root@$(ROUTER):$(MVC)/models/
	scp -r src/opnsense/mvc/app/views/OPNsense \
		root@$(ROUTER):$(MVC)/views/
	scp src/opnsense/service/conf/actions.d/actions_cloudflared.conf \
		root@$(ROUTER):$(SERVICE)/
	ssh root@$(ROUTER) "configctl template reload OPNsense/Cloudflared || true"
	ssh root@$(ROUTER) "service configd restart"
	@echo "Plugin deployed to $(ROUTER)"

## Run go vet on all packages.
lint:
	$(GO) vet ./...

## Format all Go source.
fmt:
	gofmt -w .

## Remove compiled artifacts.
clean:
	rm -rf $(BUILD_DIR)
