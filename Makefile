.PHONY: build build-ui run test test-ui fmt vet package package-host verify-package verify-package-host clean

# Keep BIN/VERSION in sync with manifest.yaml.
BIN := bin/kandev-plugin-gitea
VERSION := 0.1.0
STAGE := .build/stage
PKG_OUT := kandev-plugin-gitea-$(VERSION).tar.gz
# Packaging only: a kandev checkout (v0.102.0+) providing cmd/plugin-pack. The SDK itself
# comes from the published modules. Override: make package KANDEV_BACKEND=/path/apps/backend
KANDEV_BACKEND ?= ../kandev/apps/backend

build: build-ui
	mkdir -p bin
	go build -o $(BIN) ./server/...

build-ui:
	npm run build:ui

run: build
	./$(BIN)

test: test-ui
	go test ./...

test-ui:
	npm test

fmt:
	gofmt -l .

vet:
	go vet ./...

define stage_package
	rm -rf $(STAGE)
	mkdir -p $(STAGE)/server $(STAGE)/ui
	cp manifest.yaml $(STAGE)/manifest.yaml
	cp -r assets $(STAGE)/assets
	cp ui/bundle.js $(STAGE)/ui/bundle.js
endef

## Cross-compile every executable in manifest.yaml and pack with Kandev's plugin-pack.
package: build-ui
	$(stage_package)
	GOOS=linux   GOARCH=amd64 go build -o $(STAGE)/server/plugin-linux-amd64       ./server
	GOOS=linux   GOARCH=arm64 go build -o $(STAGE)/server/plugin-linux-arm64       ./server
	GOOS=darwin  GOARCH=amd64 go build -o $(STAGE)/server/plugin-darwin-amd64      ./server
	GOOS=darwin  GOARCH=arm64 go build -o $(STAGE)/server/plugin-darwin-arm64      ./server
	GOOS=windows GOARCH=amd64 go build -o $(STAGE)/server/plugin-windows-amd64.exe ./server
	find $(STAGE) -name .DS_Store -delete
	go -C $(KANDEV_BACKEND) run ./cmd/plugin-pack -dir $(abspath $(STAGE)) -out $(abspath $(PKG_OUT))
	rm -rf $(STAGE)
	@echo "Wrote $(PKG_OUT)"

## Host platform only — faster local iteration.
package-host: build-ui
	$(stage_package)
	go build -o $(STAGE)/server/plugin-$$(go env GOOS)-$$(go env GOARCH)$$(go env GOEXE) ./server
	find $(STAGE) -name .DS_Store -delete
	go -C $(KANDEV_BACKEND) run ./cmd/plugin-pack -dir $(abspath $(STAGE)) -out $(abspath $(PKG_OUT)) -platform-only
	rm -rf $(STAGE)
	@echo "Wrote $(PKG_OUT)"

define verify_archive
set -eu; \
VERIFY_DIR="$$(mktemp -d)"; \
trap 'rm -rf "$$VERIFY_DIR"' EXIT; \
tar -xzf "$(PKG_OUT)" -C "$$VERIFY_DIR"; \
test -f "$$VERIFY_DIR/manifest.yaml"; \
test -f "$$VERIFY_DIR/assets/icon.svg"; \
test -f "$$VERIFY_DIR/ui/bundle.js"; \
test -f "$$VERIFY_DIR/checksums.txt"; \
test ! -e "$$VERIFY_DIR/ui/src"; \
if command -v sha256sum >/dev/null 2>&1; then \
	(cd "$$VERIFY_DIR" && sha256sum -c checksums.txt); \
else \
	(cd "$$VERIFY_DIR" && shasum -a 256 -c checksums.txt); \
fi; \
$(1)
endef

verify-package: package
	@$(call verify_archive,for executable in server/plugin-linux-amd64 server/plugin-linux-arm64 server/plugin-darwin-amd64 server/plugin-darwin-arm64 server/plugin-windows-amd64.exe; do test -f "$$VERIFY_DIR/$$executable" || { echo "package missing $$executable"; exit 1; }; done)

verify-package-host: package-host
	@$(call verify_archive,test -f "$$VERIFY_DIR/server/plugin-$$(go env GOOS)-$$(go env GOARCH)$$(go env GOEXE)" || { echo "package missing host executable"; exit 1; })

clean:
	rm -rf bin .build kandev-plugin-gitea-*.tar.gz
