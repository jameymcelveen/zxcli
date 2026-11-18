SHELL   := /bin/sh
BIN     := zx
MODULE  := github.com/jameymcelveen/zxcli
DIST    := dist
PREFIX  ?= /usr/local

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Installers need a plain numeric version: v1.2.3-4-gabc -> 1.2.3
PKG_VERSION := $(shell v=$$(echo "$(VERSION)" | sed -e 's/^v//' -e 's/-.*//'); case "$$v" in ([0-9]*.[0-9]*) echo "$$v";; (*) echo 0.0.0;; esac)

LDFLAGS   := -s -w -X $(MODULE)/core.Version=$(VERSION) -X $(MODULE)/core.Commit=$(COMMIT) -X $(MODULE)/core.Date=$(DATE)
BUILD     := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
NFPM      ?= go run github.com/goreleaser/nfpm/v2/cmd/nfpm@latest

.DEFAULT_GOAL := help
print-version: ## Show the version numbers the build will use
	@echo "$(VERSION) -> package version $(PKG_VERSION)"

.PHONY: print-version help build run test cover fmt lint check install uninstall new \
        dist archives linux-packages pkg upload-pkg windows-installer installers checksums clean

help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## --- everyday -------------------------------------------------------------

build: ## Build bin/zx for this machine
	$(BUILD) -o bin/$(BIN) ./cmd/zx

run: ## Run without installing: make run ARGS="guid 3"
	@go run ./cmd/zx $(ARGS)

test: ## Run the tests
	go test ./...

cover: ## Run the tests and show coverage per package
	go test -cover ./...

fmt: ## Format the Go code
	gofmt -w .

lint: ## Check formatting and vet for every OS
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi
	go vet ./...
	GOOS=windows go vet ./...

check: lint test ## Lint and test: run this before pushing

install: build ## Install zx to $(PREFIX)/bin
	install -d $(PREFIX)/bin
	install -m 755 bin/$(BIN) $(PREFIX)/bin/$(BIN)
	@echo "installed $$($(PREFIX)/bin/$(BIN) version)"

uninstall: ## Remove zx from $(PREFIX)/bin
	rm -f $(PREFIX)/bin/$(BIN)

new: ## Scaffold a command: make new NAME=dice
	@[ -n "$(NAME)" ] || { echo "usage: make new NAME=dice"; exit 1; }
	@go run ./cmd/zx new $(NAME)

## --- release --------------------------------------------------------------

dist: ## Cross-compile for Linux, macOS and Windows
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; \
		if [ "$$os" = windows ]; then ext=".exe"; fi; \
		out=$(DIST)/$(BIN)_$${os}_$${arch}/$(BIN)$$ext; \
		echo "  build   $$out"; \
		GOOS=$$os GOARCH=$$arch $(BUILD) -o $$out ./cmd/zx || exit 1; \
	done

archives: dist ## .tar.gz (Linux, macOS) and .zip (Windows) of each build
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; d=$(BIN)_$${os}_$${arch}; \
		cp LICENSE README.md $(DIST)/$$d/; \
		if [ "$$os" = windows ]; then \
			rm -f $(DIST)/$$d.zip; \
			(cd $(DIST)/$$d && zip -q ../$$d.zip $(BIN).exe LICENSE README.md) || exit 1; \
			echo "  archive $(DIST)/$$d.zip"; \
		else \
			tar -C $(DIST)/$$d -czf $(DIST)/$$d.tar.gz $(BIN) LICENSE README.md || exit 1; \
			echo "  archive $(DIST)/$$d.tar.gz"; \
		fi; \
	done

linux-packages: dist ## .deb and .rpm installers (uses nfpm through "go run")
	@for arch in amd64 arm64; do \
		sed -e "s|@VERSION@|$(PKG_VERSION)|" -e "s|@ARCH@|$$arch|" \
			-e "s|@BIN@|$(DIST)/$(BIN)_linux_$$arch/$(BIN)|" packaging/nfpm.yaml > $(DIST)/nfpm_$$arch.yaml; \
		for fmt in deb rpm; do \
			$(NFPM) pkg --config $(DIST)/nfpm_$$arch.yaml --packager $$fmt \
				--target $(DIST)/$(BIN)_linux_$$arch.$$fmt || exit 1; \
		done; \
	done

pkg: dist ## macOS .pkg installer, universal binary (macOS only)
	@[ "$$(uname)" = Darwin ] || { echo "the .pkg installer can only be built on macOS"; exit 1; }
	rm -rf $(DIST)/pkgroot && mkdir -p $(DIST)/pkgroot/usr/local/bin
	lipo -create -output $(DIST)/pkgroot/usr/local/bin/$(BIN) \
		$(DIST)/$(BIN)_darwin_amd64/$(BIN) $(DIST)/$(BIN)_darwin_arm64/$(BIN)
	pkgbuild --root $(DIST)/pkgroot --identifier com.jameymcelveen.zx \
		--version $(PKG_VERSION) --install-location / $(DIST)/$(BIN)_darwin_universal.pkg

upload-pkg: pkg ## Attach the macOS .pkg to a GitHub release: make upload-pkg VERSION=v0.1.0
	cd $(DIST) && gh release download $(VERSION) --pattern checksums.txt --clobber
	cd $(DIST) && { grep -v '$(BIN)_darwin_universal.pkg' checksums.txt; shasum -a 256 $(BIN)_darwin_universal.pkg; } > checksums.tmp && mv checksums.tmp checksums.txt
	gh release upload $(VERSION) $(DIST)/$(BIN)_darwin_universal.pkg $(DIST)/checksums.txt --clobber

windows-installer: dist ## Windows setup .exe (needs makensis)
	@command -v makensis >/dev/null || { echo "makensis not found. macOS: brew install makensis. Ubuntu: apt-get install nsis"; exit 1; }
	@for arch in amd64 arm64; do \
		makensis -V2 -DVERSION=$(PKG_VERSION) \
			-DBIN=$(CURDIR)/$(DIST)/$(BIN)_windows_$$arch/$(BIN).exe \
			-DOUT=$(CURDIR)/$(DIST)/$(BIN)_windows_$${arch}_setup.exe \
			packaging/windows/zx.nsi || exit 1; \
	done

installers: archives linux-packages ## Every installer this machine can build
	@if [ "$$(uname)" = Darwin ]; then $(MAKE) --no-print-directory pkg; else echo "skipped the macOS .pkg (needs macOS)"; fi
	@if command -v makensis >/dev/null; then $(MAKE) --no-print-directory windows-installer; else echo "skipped the Windows setup .exe (needs makensis)"; fi
	@$(MAKE) --no-print-directory checksums
	@ls -1 $(DIST) | grep -E '\.(tar\.gz|zip|deb|rpm|pkg|exe|txt)$$'

checksums: ## Write dist/checksums.txt
	@cd $(DIST) && rm -f checksums.txt && for f in *.tar.gz *.zip *.deb *.rpm *.pkg *_setup.exe; do \
		if [ -f "$$f" ]; then { shasum -a 256 "$$f" 2>/dev/null || sha256sum "$$f"; } >> checksums.txt; fi; \
	done

clean: ## Delete build output
	rm -rf bin $(DIST) coverage.out
