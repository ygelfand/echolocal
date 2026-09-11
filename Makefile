.DEFAULT_GOAL := help

GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

BASE := $(shell cat VERSION 2>/dev/null || echo 0.0.0)
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null)
EPOCH := $(shell date -u +%s)
BUILT_FROM := $(shell git status --porcelain 2>/dev/null | grep -q . && echo dirty || echo $(GIT_COMMIT))
VERSION ?= $(if $(GIT_TAG),$(GIT_TAG),$(BASE)-dev.$(EPOCH)_$(BUILT_FROM))

REPO ?= ygelfand/echolocal
RELEASES ?= https://github.com/$(REPO)/releases

BUILDVARS := github.com/ygelfand/echolocal/internal/layout
LDFLAGS := -X '$(BUILDVARS).Version=$(VERSION)' \
	-X '$(BUILDVARS).GitCommit=$(GIT_COMMIT)' \
	-X '$(BUILDVARS).BuildDate=$(BUILD_DATE)'

BUILD_DIR := bin
ASSET_DIR := internal/host/assets/payload

# echod targets the Echo Dot 2: MT8163, Android 5.1 (API 22). FireOS 5 runs an arm64 kernel; FireOS 6
# ships a 32-bit kernel on the same hardware, which cannot exec arm64 at all.
# The ALSA path is pure Go over /dev/snd ioctls, so no cgo and no NDK. Keep it that way unless
# something genuinely needs C, which is what would make a toolchain image worth having.
ARCHES := arm64 arm
DOT_ARCH ?= arm
DEVICE_ENV := GOOS=linux GOARCH=$(DOT_ARCH) CGO_ENABLED=0
DEVICE_LDFLAGS := -s -w $(LDFLAGS)
DEVICE_BIN := $(BUILD_DIR)/echod-$(DOT_ARCH)

# TAGS passes build tags through to echod, which is how the same device can be measured both ways:
# `make install-echod TAGS=noasm` builds the portable dot instead of the NEON one.
TAGS ?=
DEVICE_TAGS := $(if $(TAGS),-tags $(TAGS),)

ADB ?= adb
DEVICE_TMP := /data/local/tmp

# echod lives under /system/app because that tree is labelled u:object_r:system_file:s0,
# the label that leaves an init-started service in init's own domain rather than the narrow
# per-service domain its stock *_exec label would select.
#
# It is installed as Amazon's ledcontroller service: that kills the spinning ring by removing
# its driver, and init then starts echod from on post-fs-data and restarts it if it exits.
ECHOD_DIR := /system/app/echod
STATE_DIR := /data/misc/echolocal
LEDD := /system/bin/ledcontroller
LEDD_LABEL_ORIG := u:object_r:ledd_exec:s0

##@ Development

.PHONY: build
build: build-echoctl build-echod ## Build both binaries

.PHONY: build-echoctl
build-echoctl: ## Build the host CLI into ./bin
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echoctl ./cmd/echoctl

.PHONY: build-echod
build-echod: ## Cross-compile echod for the Echo Dot (DOT_ARCH=arm64 for a Fire OS 5 kernel; TAGS=noasm for the portable dot)
	@mkdir -p $(BUILD_DIR)
	$(DEVICE_ENV) go build $(DEVICE_TAGS) -ldflags "$(DEVICE_LDFLAGS)" -o $(DEVICE_BIN) ./cmd/echod

.PHONY: build-echod-all
build-echod-all:
	@for a in $(ARCHES); do $(MAKE) --no-print-directory build-echod DOT_ARCH=$$a; done

.PHONY: run-echoctl
run-echoctl: ## Run echoctl on the host (make run-echoctl ARGS="tools tone -h")
	go run ./cmd/echoctl $(ARGS)

.PHONY: run-echod
run-echod: push-echod ## Push echod and run it (make run-echod ARGS="tools info")
	$(ADB) shell $(DEVICE_TMP)/echod $(ARGS)

.PHONY: push-echod
push-echod: build-echod ## Push echod to /data/local/tmp for iteration
	@$(ADB) push $(DEVICE_BIN) $(DEVICE_TMP)/echod >/dev/null
	@$(ADB) shell chmod 755 $(DEVICE_TMP)/echod

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and open a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: fmt
fmt: ## Format Go source
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping"; \
	fi

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: check
check: fmt vet lint test ## Format, vet, lint and test

##@ Device (echod)

.PHONY: payload
payload: ## Stage echod and the boot image for embedding into echoctl
	@$(MAKE) --no-print-directory build-echod DOT_ARCH=arm
	@mkdir -p $(ASSET_DIR)
	cp $(BUILD_DIR)/echod-arm $(ASSET_DIR)/echod
	@shasum -a 256 $(ASSET_DIR)/echod | awk '{print $$1}' > $(ASSET_DIR)/echod.sha256

.PHONY: dist
dist: payload ## Full build: echod, the boot image, then echoctl carrying both
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -tags payload -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echoctl ./cmd/echoctl

.PHONY: install-echod
install-echod: build-echod ## Install echod into /system/app/echod, restarting it if installed
# This is a manual copy, not an upgrade, so the trial an update may have left open is cleared with it.
# Otherwise the restart below looks like a binary that took an update and died without committing, and
# echod reboots the device to put the old one back — taking this install with it.
	@$(ADB) shell 'setprop ctl.stop ledcontroller; sleep 1'
	@$(ADB) remount >/dev/null
	@$(ADB) shell 'mkdir -p $(ECHOD_DIR) && rm -f $(ECHOD_DIR)/echod.prev $(ECHOD_DIR)/echod.old'
	@$(ADB) push $(DEVICE_BIN) $(ECHOD_DIR)/echod >/dev/null
	@$(ADB) shell 'chmod 755 $(ECHOD_DIR)/echod; \
		rm -f $(STATE_DIR)/updating; setprop echolocal.trial ""; setprop echolocal.rolledback ""; \
		[ -L $(LEDD) ] && setprop ctl.start ledcontroller; ls -lZ $(ECHOD_DIR)/echod'

.PHONY: install-service
install-service: install-echod ## Take over the ledcontroller service so init starts echod
	@$(ADB) remount >/dev/null
	@$(ADB) shell '[ -e $(LEDD).orig ] || mv $(LEDD) $(LEDD).orig; \
		rm -f $(LEDD); ln -s $(ECHOD_DIR)/echod $(LEDD); \
		ls -lZ $(LEDD) $(LEDD).orig'

.PHONY: uninstall-service
uninstall-service: ## Restore Amazon's ledcontroller binary and its SELinux label
	@$(ADB) remount >/dev/null
	@$(ADB) shell 'rm -f $(LEDD); mv $(LEDD).orig $(LEDD); \
		chcon $(LEDD_LABEL_ORIG) $(LEDD); ls -lZ $(LEDD)'

.PHONY: restart-echod
restart-echod: ## Restart echod through init (ctl.stop then ctl.start)
	@$(ADB) shell 'setprop ctl.stop ledcontroller; sleep 1; setprop ctl.start ledcontroller; \
		sleep 1; echo "init.svc: $$(getprop init.svc.ledcontroller)"'

.PHONY: state
state: ## Show what echod and init say about echod
	@$(ADB) shell 'echo "state:   $$(getprop echolocal.state)"; \
		echo "started: $$(getprop echolocal.started)"; \
		echo "init.svc: $$(getprop init.svc.ledcontroller)"'

.PHONY: logs
logs: ## Tail echod logs from a connected device
	$(ADB) logcat -s echolocal:*

.PHONY: shell
shell: ## Open a root shell on a connected device
	$(ADB) shell

##@ Build & Release

.PHONY: install
install: ## go install echoctl into $$GOPATH/bin
	go install -ldflags "$(LDFLAGS)" ./cmd/echoctl

AT ?= $(VERSION)
FROM ?= $(RELEASES)/download/$(AT)
PAGE ?= $(RELEASES)/tag/$(AT)

.PHONY: manifest
manifest: build-echod-all ## Write the manifest a device fetches to find this build
	@mkdir -p $(BUILD_DIR)
	go run ./cmd/mkmanifest \
		-version "$(AT)" \
		-from "$(FROM)" \
		-arm64 $(BUILD_DIR)/echod-arm64 \
		-arm $(BUILD_DIR)/echod-arm \
		-title "EchoLocal $(AT)" \
		-release-url "$(PAGE)" \
		-out $(BUILD_DIR)/manifest.json
	@cat $(BUILD_DIR)/manifest.json

.PHONY: release-dev
release-dev: ## Publish this working tree to the dev channel, without pushing anything
	@command -v gh >/dev/null || { echo "needs the gh CLI: brew install gh"; exit 1; }
	@command -v goreleaser >/dev/null || { echo "needs goreleaser: brew install goreleaser"; exit 1; }
	VERSION=$(VERSION) goreleaser release --snapshot --clean
	@for f in dist/echoctl_*/echoctl dist/echoctl_*/echoctl.exe; do \
		[ -f "$$f" ] || continue; \
		d=$$(basename $$(dirname $$f)); \
		os=$$(echo $$d | cut -d_ -f2); \
		arch=$$(echo $$d | cut -d_ -f3); \
		if [ "$$arch" = amd64 ]; then arch=x86_64; fi; \
		ext=$${f##*.}; [ "$$ext" = exe ] && ext=.exe || ext=; \
		cp "$$f" "dist/echolocal_$${os}_$${arch}$${ext}"; \
	done
	@$(MAKE) --no-print-directory manifest VERSION=$(VERSION) FROM=$(RELEASES)/download/dev PAGE=$(RELEASES)/tag/dev
	@gh release view dev >/dev/null 2>&1 || \
		gh release create dev --prerelease --title dev --notes "Rolling build for devices on the dev channel."
	gh release upload dev dist/echolocal_* $(BUILD_DIR)/echod-arm64 $(BUILD_DIR)/echod-arm $(BUILD_DIR)/manifest.json --clobber
	@echo "dev channel now serves $(VERSION)"

.PHONY: release
release: ## Release the version in VERSION
	@$(MAKE) --no-print-directory tag TAG=$(BASE)

.PHONY: tag
tag:
	@test -n "$(TAG)" || { echo "usage: make release, make release-dev, or make tag TAG=0.4.2"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "the working tree is dirty"; exit 1; }
	@git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null && { echo "$(TAG) already exists"; exit 1; } || true
	git tag -a "$(TAG)" -m "EchoLocal $(TAG)"
	git push origin "$(TAG)"
	@echo "pushed $(TAG) — the release workflow builds it from here"

.PHONY: snapshot
snapshot: ## Build a local goreleaser snapshot
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) dist/ coverage.out
	rm -rf $(ASSET_DIR)
	go clean

##@ Help

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
