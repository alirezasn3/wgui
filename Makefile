# wgui targets Linux. Building on another machine is supported so the panel can
# be developed there, but the binary that gets deployed is a Linux one.

BINARY  := wgui
DIST    := dist

# A tagged commit gives a clean version like v2.0.0; anything else is described
# relative to the last tag so a binary can always be traced back to its source.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse HEAD 2>/dev/null)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

MODULE  := wgui
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

PLATFORMS := amd64 arm64

.PHONY: all
all: build

## setup: install frontend dependencies and build a Linux binary from scratch
.PHONY: setup
setup: deps linux

## deps: install frontend dependencies
.PHONY: deps
deps:
	cd web && npm install

## web: build the frontend that gets embedded into the binary
# The build empties web/build, placeholder included; it is put back so a
# checkout without a built frontend still compiles.
.PHONY: web
web:
	cd web && npm run build && touch build/.gitkeep

## build: build for this machine
.PHONY: build
build: web
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/wgui

## linux: build for a Linux server (ARCH=amd64 or arm64)
ARCH ?= amd64
.PHONY: linux
linux: web
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 \
		go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux-$(ARCH) ./cmd/wgui
	@echo "built $(BINARY)-linux-$(ARCH) ($(VERSION))"

## dist: build every Linux architecture into dist/, with checksums
.PHONY: dist
dist: web
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for arch in $(PLATFORMS); do \
		echo "building $(BINARY)-linux-$$arch"; \
		GOOS=linux GOARCH=$$arch CGO_ENABLED=0 \
			go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-linux-$$arch ./cmd/wgui || exit 1; \
	done
	@cp config.example.json $(DIST)/
	@cd $(DIST) && shasum -a 256 * > SHA256SUMS
	@echo
	@ls -lh $(DIST)

## release: tag the current commit and push it, which builds the release
.PHONY: release
release:
	@test -n "$(TAG)" || { echo "usage: make release TAG=v2.0.0"; exit 1; }
	@echo "$(TAG)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' \
		|| { echo "TAG must look like v2.0.0 or v2.0.0-rc1"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree is dirty; commit first"; exit 1; }
	@grep -q "^## $(TAG)$$" CHANGELOG.md \
		|| { echo "CHANGELOG.md has no '## $(TAG)' section; it becomes the release notes"; exit 1; }
	git tag -a $(TAG) -m "$(TAG)"
	git push origin $(TAG)
	@echo "pushed $(TAG); the release workflow builds and publishes the binaries"

## test: run the Go tests and the frontend type check
.PHONY: test
test:
	go test ./...
	cd web && npm run check

## dev: run against a fake WireGuard interface over plain HTTP
.PHONY: dev
dev: build
	./$(BINARY) --fake-wg --dev-http

.PHONY: fmt
fmt:
	go fmt ./...
	cd web && npm run format

.PHONY: clean
clean:
	rm -f $(BINARY) $(BINARY)-linux-*
	rm -rf $(DIST) web/.svelte-kit
	rm -rf web/build/* && touch web/build/.gitkeep

## version: show what would be stamped into a build
.PHONY: version
version:
	@echo "$(VERSION) ($(COMMIT))"

## help: list targets
.PHONY: help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
