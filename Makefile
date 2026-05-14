# cloudflared-opnsense: OPNsense plugin with vendored Mk/plugins.mk
#
# OPNsense targets (install, metadata, opnsense-package) follow upstream layout.
# Go targets build cloudflared-configd and cloudflared-builder.
#
# Usage:
#   make | make build          Build Go binaries for the host
#   make freebsd               Cross-compile Go binaries for FreeBSD amd64
#   make install DESTDIR=...   Stage plugin files under DESTDIR (see Mk/plugins.mk)
#   make metadata DESTDIR=...  Generate +MANIFEST, +DESC, plist under DESTDIR
#   make opnsense-package      Full pkg create (needs deps; CI uses cloudflared-builder)
#   make release               Build freebsd then run cloudflared-builder run
#   make deploy-live           Push plugin to a live router over SSH (dev only)
#   make clean                 Remove dist/ and work/
#   make lint | make fmt       Go quality

PLUGIN_NAME=		cloudflared
PLUGIN_VERSION?=	2026.3.0
PLUGIN_REVISION?=	0
PLUGIN_COMMENT=		OPNsense plugin for Cloudflare Tunnel
PLUGIN_MAINTAINER=	alex@goodkind.io
PLUGIN_WWW=		https://goodkind.io
PLUGIN_LICENSE=		BSD2CLAUSE

# Vendored Mk/; opnsense-scripts/ and opnsense-templates/ (see Mk/defaults.mk).
PLUGINSDIR=		${.CURDIR}

# Satisfy defaults.mk when php/python are not on PATH (e.g. CI builder VM).
PLUGIN_PHP?=		82
PLUGIN_PYTHON?=		311

.include "Mk/plugins.mk"

GO=		go
GOOS_BSD=	freebsd
GOARCH=		amd64

BUILD_DIR=	dist
CMD_CONFIGD=	cmd/cloudflared-configd
CMD_BUILDER=	cmd/cloudflared-builder

CONFIGD_BIN=	${BUILD_DIR}/cloudflared-configd
BUILDER_BIN=	${BUILD_DIR}/cloudflared-builder

PREFIX=		/usr/local
SERVICE=	${PREFIX}/opnsense/service/conf/actions.d
MVC=		${PREFIX}/opnsense/mvc/app

.PHONY: all build freebsd clean lint fmt release deploy-live

.DEFAULT_GOAL:=	build

all: build

build:
	@mkdir -p ${BUILD_DIR}
	${GO} build -o ${CONFIGD_BIN} ./${CMD_CONFIGD}
	${GO} build -o ${BUILDER_BIN} ./${CMD_BUILDER}

freebsd:
	@mkdir -p ${BUILD_DIR}
	GOOS=${GOOS_BSD} GOARCH=${GOARCH} \
		${GO} build -o ${CONFIGD_BIN} ./${CMD_CONFIGD}
	GOOS=${GOOS_BSD} GOARCH=${GOARCH} \
		${GO} build -o ${BUILDER_BIN} ./${CMD_BUILDER}
	@echo "FreeBSD binaries written to ${BUILD_DIR}/"

release: freebsd
	${BUILDER_BIN} run

ROUTER?=	3d06:bad:b01::1

# Deploy to a live OPNsense host (development iteration; not DESTDIR staging).
deploy-live: freebsd
	ssh root@${ROUTER} "mkdir -p ${PREFIX}/bin ${PREFIX}/etc/rc.d"
	scp ${CONFIGD_BIN} root@${ROUTER}:${PREFIX}/bin/cloudflared-configd
	scp src/etc/rc.d/cloudflared root@${ROUTER}:${PREFIX}/etc/rc.d/cloudflared
	scp -r src/opnsense/mvc/app/controllers/OPNsense \
		root@${ROUTER}:${MVC}/controllers/
	scp -r src/opnsense/mvc/app/models/OPNsense \
		root@${ROUTER}:${MVC}/models/
	scp -r src/opnsense/mvc/app/views/OPNsense \
		root@${ROUTER}:${MVC}/views/
	scp src/opnsense/service/conf/actions.d/actions_cloudflared.conf \
		root@${ROUTER}:${SERVICE}/
	ssh root@${ROUTER} "configctl template reload OPNsense/Cloudflared || true"
	ssh root@${ROUTER} "service configd restart"
	@echo "Plugin deployed to ${ROUTER}"

lint:
	${GO} vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf ${BUILD_DIR}
	rm -rf ${.CURDIR}/work
