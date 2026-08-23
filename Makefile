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
BOOT_IMAGE := images/echolocal-boot.img
PRYON_APK ?= android/pryon/build/EchoLocalPryon.apk
ANDROID_MEDIA ?= android/amazon-helper/build/amazon-helper.jar

# echod targets the Echo Dot 2: MT8163, Android 5.1 (API 22). Amazon ships a 32-bit userspace but
# the SoC and kernel are arm64 and /system/lib64 is present, so echod is built 64-bit: the wake word
# pipeline costs 50ms per 80ms of audio there against 88ms as 32-bit code, which does not fit.
# The ALSA path is pure Go over /dev/snd ioctls, so no cgo and no NDK. Keep it that way unless
# something genuinely needs C, which is what would make a toolchain image worth having.
DEVICE_ENV := GOOS=linux GOARCH=arm64 CGO_ENABLED=0
DEVICE_LDFLAGS := -s -w $(LDFLAGS)

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
build-echod: ## Cross-compile echod for the Echo Dot (static, no cgo; TAGS=noasm for the portable dot)
	@mkdir -p $(BUILD_DIR)
	$(DEVICE_ENV) go build $(DEVICE_TAGS) -ldflags "$(DEVICE_LDFLAGS)" -o $(BUILD_DIR)/echod ./cmd/echod

.PHONY: run-echoctl
run-echoctl: ## Run echoctl on the host (make run-echoctl ARGS="tools tone -h")
	go run ./cmd/echoctl $(ARGS)

.PHONY: run-echod
run-echod: push-echod ## Push echod and run it (make run-echod ARGS="tools info")
	$(ADB) shell $(DEVICE_TMP)/echod $(ARGS)

.PHONY: push-echod
push-echod: build-echod ## Push echod to /data/local/tmp for iteration
	@$(ADB) push $(BUILD_DIR)/echod $(DEVICE_TMP)/echod >/dev/null
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
payload: build-echod ## Stage echod and the boot image for embedding into echoctl
	@mkdir -p $(ASSET_DIR)
	@test -f $(PRYON_APK) || { echo "missing $(PRYON_APK); build android/pryon first"; exit 1; }
	@test -f $(ANDROID_MEDIA) || { echo "missing $(ANDROID_MEDIA); build android/amazon-helper first"; exit 1; }
	cp $(BUILD_DIR)/echod $(ASSET_DIR)/echod
	cp $(BOOT_IMAGE) $(ASSET_DIR)/boot.img
	cp $(PRYON_APK) $(ASSET_DIR)/EchoLocalPryon.apk
	cp $(ANDROID_MEDIA) $(ASSET_DIR)/amazon-helper.jar
	@shasum -a 256 $(ASSET_DIR)/echod | awk '{print $$1}' > $(ASSET_DIR)/echod.sha256
	@shasum -a 256 $(ASSET_DIR)/boot.img | awk '{print $$1}' > $(ASSET_DIR)/boot.img.sha256
	@shasum -a 256 $(ASSET_DIR)/EchoLocalPryon.apk | awk '{print $$1}' > $(ASSET_DIR)/EchoLocalPryon.apk.sha256
	@shasum -a 256 $(ASSET_DIR)/amazon-helper.jar | awk '{print $$1}' > $(ASSET_DIR)/amazon-helper.jar.sha256

.PHONY: dist
dist: payload ## Full build: echod, the boot image, then echoctl carrying both
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -tags payload -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echoctl ./cmd/echoctl

.PHONY: install-echod
install-echod: build-echod ## Install echod into /system/app/echod, restarting it if installed
# This is a manual copy, not an upgrade, so the trial an update may have left open is cleared with it.
# Otherwise the restart below looks like a binary that took an update and died without committing, and
# echod reboots the device to put the old one back — taking this install with it.
	@$(ADB) shell 'setprop ctl.stop ledcontroller; sleep 1; \
		mount -o remount,rw /system && mkdir -p $(ECHOD_DIR) && \
		rm -f $(ECHOD_DIR)/echod.prev $(ECHOD_DIR)/echod.old'
	@$(ADB) push $(BUILD_DIR)/echod $(ECHOD_DIR)/echod >/dev/null
	@$(ADB) shell 'chmod 755 $(ECHOD_DIR)/echod; mount -o remount,ro /system; \
		rm -f $(STATE_DIR)/updating; setprop echolocal.trial ""; setprop echolocal.rolledback ""; \
		[ -L $(LEDD) ] && setprop ctl.start ledcontroller; ls -lZ $(ECHOD_DIR)/echod'

.PHONY: install-service
install-service: install-echod ## Take over the ledcontroller service so init starts echod
	@$(ADB) shell 'mount -o remount,rw /system; \
		[ -e $(LEDD).orig ] || mv $(LEDD) $(LEDD).orig; \
		rm -f $(LEDD); ln -s $(ECHOD_DIR)/echod $(LEDD); \
		mount -o remount,ro /system; ls -lZ $(LEDD) $(LEDD).orig'

.PHONY: uninstall-service
uninstall-service: ## Restore Amazon's ledcontroller binary and its SELinux label
	@$(ADB) shell 'mount -o remount,rw /system; rm -f $(LEDD); mv $(LEDD).orig $(LEDD); \
		chcon $(LEDD_LABEL_ORIG) $(LEDD); mount -o remount,ro /system; ls -lZ $(LEDD)'

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
manifest: build-echod ## Write the manifest a device fetches to find this build
	@mkdir -p $(BUILD_DIR)
	go run ./cmd/mkmanifest \
		-version "$(AT)" \
		-file $(BUILD_DIR)/echod \
		-url "$(FROM)/echod" \
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
	gh release upload dev dist/echolocal_* $(BUILD_DIR)/echod $(BUILD_DIR)/manifest.json --clobber
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
