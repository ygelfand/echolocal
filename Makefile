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
BOARDVARS := github.com/ygelfand/echolocal/internal/board
LDFLAGS := -X '$(BUILDVARS).Version=$(VERSION)' \
	-X '$(BUILDVARS).GitCommit=$(GIT_COMMIT)' \
	-X '$(BUILDVARS).BuildDate=$(BUILD_DATE)' \
	$(if $(COUNTERTOP_FILES_URL),-X 'github.com/ygelfand/libcountertop/pkg/network/releases.files=$(COUNTERTOP_FILES_URL)')

BUILD_DIR := bin

UPX_VERSION := 5.3.0
UPX_DIR := $(BUILD_DIR)/upx-$(UPX_VERSION)
UPX := $(UPX_DIR)/build/upx

# Echo Dot 2: MT8163, Android 5.1 (API 22); Fire OS 5 runs an arm64 kernel, Fire OS 6 a 32-bit one that cannot exec arm64.
ARCHES := arm64 arm

BOARD_ARCHES := arm

DOT_ARCH ?= arm
DEVICE_ENV := GOOS=linux GOARCH=$(DOT_ARCH) CGO_ENABLED=0

BOARD ?=

SCREEN_BOARDS := checkers cronos crown athena
BOARD_BUILDS := $(SCREEN_BOARDS)

TAGS ?=
PACK ?=
empty :=
space := $(empty) $(empty)
comma := ,
ALL_TAGS := $(strip $(TAGS) board_$(or $(BOARD),biscuit) airplay pryon $(if $(filter $(BOARD),$(SCREEN_BOARDS)),voice payload))
DEVICE_TAGS := $(if $(ALL_TAGS),-tags $(subst $(space),$(comma),$(ALL_TAGS)),)

DEVICE_LDFLAGS := -s -w $(LDFLAGS) $(if $(BOARD),-X '$(BOARDVARS).pinned=$(BOARD)')
DEVICE_BIN := $(BUILD_DIR)/echod$(if $(BOARD),-$(BOARD))-$(DOT_ARCH)

ADB ?= adb
DEVICE_TMP := /data/local/tmp

DEVICE ?=
ifneq ($(DEVICE),)
ADB := $(ADB) -s $(DEVICE)
SERIAL := --serial $(DEVICE)
endif

BOARD_SH = go run ./cmd/echolocal board --sh $(SERIAL)

# /system/app is labelled u:object_r:system_file:s0.
ECHOD_DIR := /system/app/echod
STATE_DIR := /data/misc/echolocal

NDK ?= $(firstword $(ANDROID_NDK_HOME) $(ANDROID_NDK_LATEST_HOME) $(wildcard /opt/homebrew/Caskroom/android-ndk/*/AndroidNDK*.app/Contents/NDK))
NDK_HOST := $(if $(filter Darwin,$(shell uname -s)),darwin-x86_64,linux-x86_64)
NATIVE_API = $(shell go run ./cmd/native-target $(or $(BOARD),biscuit))
NDK_CC = $(NDK)/toolchains/llvm/prebuilt/$(NDK_HOST)/bin/armv7a-linux-androideabi$(NATIVE_API)-clang
LIBCOUNTERTOP_MODULE := github.com/ygelfand/libcountertop
LIBCOUNTERTOP_DIR = $(shell go list -m -f '{{.Dir}}' $(LIBCOUNTERTOP_MODULE))
SURFACE_SRC = $(LIBCOUNTERTOP_DIR)/native/surface
SURFACE_BIN = $(BUILD_DIR)/echolocal-surface-api$(NATIVE_API)
CAMSHIM_BIN = $(BUILD_DIR)/libecholocal-camshim-api$(NATIVE_API).so
CAMERA_BIN = $(BUILD_DIR)/echolocal-camera-api$(NATIVE_API)
ASR_BIN = $(BUILD_DIR)/echolocal-asr-api$(NATIVE_API)
AIRPLAY_BIN = $(BUILD_DIR)/echolocal-airplay-api$(NATIVE_API)
PRYON_BIN = $(BUILD_DIR)/echolocal-pryon-api$(NATIVE_API)
COUNTERTOP_NATIVE = $(MAKE) -f "$(LIBCOUNTERTOP_DIR)/native/Makefile" API=$(NATIVE_API) PREFIX=echolocal OUT="$(abspath $(BUILD_DIR))" NDK="$(NDK)" SURFACE_BIN="$(abspath $(SURFACE_BIN))" CAMERA_BIN="$(abspath $(CAMERA_BIN))" CAMSHIM_BIN="$(abspath $(CAMSHIM_BIN))" ASR_BIN="$(abspath $(ASR_BIN))" AIRPLAY_BIN="$(abspath $(AIRPLAY_BIN))" PRYON_BIN="$(abspath $(PRYON_BIN))"
PARTS_DIR := internal/parts/payload
PART_BOARDS := $(SCREEN_BOARDS)

##@ Development

.PHONY: build
build: build-echolocal build-echod ## Build both binaries

# go list may know the version before its source archive has been downloaded.
.PHONY: countertop-native-source
countertop-native-source:
	@test -n "$(LIBCOUNTERTOP_DIR)" || go mod download $(LIBCOUNTERTOP_MODULE)

.PHONY: build-surface
build-surface: countertop-native-source ## Build the SurfaceFlinger helper with the NDK
	+$(COUNTERTOP_NATIVE) surface

.PHONY: build-camshim
build-camshim: countertop-native-source ## Build the camera service preload with the NDK
	+$(COUNTERTOP_NATIVE) camshim

.PHONY: build-camera
build-camera: countertop-native-source ## Build the camera helper with the NDK
	+$(COUNTERTOP_NATIVE) camera

.PHONY: test-surface
test-surface: countertop-native-source ## Run the shared native wire tests with the host compiler
	+$(COUNTERTOP_NATIVE) test HOST_CC="$(or $(HOST_CC),cc)"

.PHONY: build-echolocal
build-echolocal: ## Build the host CLI into ./bin
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echolocal ./cmd/echolocal

.PHONY: build-echod
build-echod: stage-airplay stage-pryon $(if $(filter $(BOARD),$(PART_BOARDS)),stage-speech stage-parts) ##Cross-compile echod for the Echo Dot (DOT_ARCH=arm64 for a Fire OS 5 kernel; TAGS=noasm for the portable dot; PACK=bin/upx-5.3.0/build/upx to compress it)
	@mkdir -p $(BUILD_DIR)
	$(DEVICE_ENV) go build $(DEVICE_TAGS) -ldflags "$(DEVICE_LDFLAGS)" -o $(DEVICE_BIN) ./cmd/echod
	$(if $(PACK),$(PACK) -q $(DEVICE_BIN))
	$(if $(PACK),$(firstword $(PACK)) -q -t $(DEVICE_BIN))

.PHONY: upx
upx: $(UPX) ## Build the pinned UPX that packs device binaries

$(UPX):
	@command -v cmake >/dev/null || { echo "needs cmake: brew install cmake"; exit 1; }
	@mkdir -p $(UPX_DIR)
	curl -sSfL https://github.com/upx/upx/releases/download/v$(UPX_VERSION)/upx-$(UPX_VERSION)-src.tar.xz | tar -xJ -C $(UPX_DIR) --strip-components=1
	cmake -S $(UPX_DIR) -B $(UPX_DIR)/build -DCMAKE_BUILD_TYPE=Release >/dev/null
	cmake --build $(UPX_DIR)/build -j >/dev/null

.PHONY: stage-speech
stage-speech: countertop-native-source ## Build and stage the speech recognizer helper echod embeds
	+$(COUNTERTOP_NATIVE) asr
	@mkdir -p $(PARTS_DIR)
	cp $(ASR_BIN) $(PARTS_DIR)/echolocal-asr

.PHONY: stage-airplay
stage-airplay: countertop-native-source ## Build and stage the AirPlay mirroring helper echod embeds
	+$(COUNTERTOP_NATIVE) airplay
	@mkdir -p $(PARTS_DIR)
	cp $(AIRPLAY_BIN) $(PARTS_DIR)/echolocal-airplay

.PHONY: stage-pryon
stage-pryon: countertop-native-source ## Build and stage the Pryon wake word helper echod embeds
	+$(COUNTERTOP_NATIVE) pryon
	@mkdir -p $(PARTS_DIR)
	cp $(PRYON_BIN) $(PARTS_DIR)/echolocal-pryon

.PHONY: stage-parts
stage-parts: build-surface build-camshim build-camera ## Stage the native parts echod embeds for the boards that carry them
	@mkdir -p $(PARTS_DIR)
	cp $(SURFACE_BIN) $(PARTS_DIR)/echolocal-surface
	cp $(CAMSHIM_BIN) $(PARTS_DIR)/libecholocal-camshim.so
	cp $(CAMERA_BIN) $(PARTS_DIR)/echolocal-camera

.PHONY: build-echod-all
build-echod-all: ## Build every binary a release publishes, from a clean bin
	@rm -f $(BUILD_DIR)/echod-*
	@for a in $(ARCHES); do $(MAKE) --no-print-directory build-echod DOT_ARCH=$$a; done
	@for b in $(BOARD_BUILDS); do \
		for a in $(BOARD_ARCHES); do $(MAKE) --no-print-directory build-echod DOT_ARCH=$$a BOARD=$$b; done; \
	done

.PHONY: run-echolocal
run-echolocal: ## Run the host CLI (make run-echolocal ARGS="tools tone -h")
	go run ./cmd/echolocal $(ARGS)

.PHONY: run-echod
run-echod: push-echod ## Push echod and run it (make run-echod ARGS="tools info")
	$(ADB) shell $(DEVICE_TMP)/echod $(ARGS)

.PHONY: push-echod
push-echod: build-echod ## Push echod to /data/local/tmp for iteration
	@$(ADB) push $(DEVICE_BIN) $(DEVICE_TMP)/echod >/dev/null
	@$(ADB) shell chmod 755 $(DEVICE_TMP)/echod

.PHONY: test
test: ## Run tests, for the Shows and for biscuit
	go test ./...
	go test -tags board_biscuit ./...

.PHONY: test-race
test-race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and open a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: fmt
fmt: ## Format Go source
	golangci-lint fmt

.PHONY: fmt-check
fmt-check: ## Fail when any Go source is unformatted
	golangci-lint fmt --diff

.PHONY: vet
vet: ## Run go vet, for the Shows and for biscuit
	go vet ./...
	go vet -tags board_biscuit ./...

.PHONY: lint
lint: ## Run golangci-lint
	@if command -v golangci-lint >/dev/null; then \
		golangci-lint run && golangci-lint run --build-tags board_biscuit; \
	else \
		echo "golangci-lint not installed, skipping"; \
	fi

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

COUNTERTOP ?= main

.PHONY: countertop
countertop: ## Point go.mod at a pushed libcountertop commit (COUNTERTOP=main, a branch, a commit or a tag)
	GOWORK=off go get $(LIBCOUNTERTOP_MODULE)@$(COUNTERTOP)
	GOWORK=off go mod tidy
	@GOWORK=off go list -m $(LIBCOUNTERTOP_MODULE)

.PHONY: check
check: fmt vet lint test ## Format, vet, lint and test

##@ Device (echod)

.PHONY: device
device: ## Say which device the device targets would write to, and refuse an unknown one
	@n=$$($(ADB) devices | grep -c "device$$"); \
	if [ "$$n" -eq 0 ]; then echo "no device attached"; exit 1; fi; \
	if [ "$$n" -gt 1 ] && [ -z "$(DEVICE)" ]; then \
		echo "$$n devices attached, so name one:"; \
		$(ADB) devices -l | grep "device " | sed 's/^/  make DEVICE=/;s/\tdevice //'; \
		exit 1; \
	fi
	@$(BOARD_SH) >/dev/null || { \
		echo "these targets remount /system, so an unrecognised device is refused"; exit 1; }
	@go run ./cmd/echolocal board $(SERIAL)

.PHONY: dist
dist: ## Every echod, a manifest naming them in bin/, and an echolocal CLI that installs from it
	@$(MAKE) --no-print-directory manifest FROM=file://$(CURDIR)/$(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echolocal ./cmd/echolocal

.PHONY: install
install: dist ## Install onto an attached device from the locally built manifest
	$(BUILD_DIR)/echolocal install --manifest $(BUILD_DIR)/manifest.json $(ARGS)

.PHONY: install-echod
install-echod: ## Install echod into /system/app/echod, built for whatever board is attached
	@$(MAKE) --no-print-directory device
	@eval "$$($(BOARD_SH))"; \
		board=$$(echo "$(BOARD_BUILDS)" | tr ' ' '\n' | grep -x "$$codename" || true); \
		$(MAKE) --no-print-directory build-echod $${board:+BOARD=$$board}; \
		bin=$(BUILD_DIR)/echod$${board:+-$$board}-$(DOT_ARCH); \
		$(ADB) shell "setprop ctl.stop $$service; sleep 1"; \
		$(ADB) remount >/dev/null; \
		$(ADB) shell 'mkdir -p $(ECHOD_DIR) && rm -f $(ECHOD_DIR)/echod.prev $(ECHOD_DIR)/echod.old $(ECHOD_DIR)/echod.aside $(STATE_DIR)/echod.prev $(STATE_DIR)/echod.old'; \
		$(ADB) push "$$bin" $(ECHOD_DIR)/echod >/dev/null; \
		$(ADB) shell "chmod 755 $(ECHOD_DIR)/echod; \
			rm -f $(STATE_DIR)/updating; setprop echolocal.trial ''; setprop echolocal.rolledback ''; \
			[ \"$$binary\" = $(ECHOD_DIR)/echod ] || [ -L \"$$binary\" ] && setprop ctl.start $$service; \
			ls -lZ $(ECHOD_DIR)/echod"

.PHONY: thumbs
thumbs: ## Render the visual picker's thumbnails on the attached screen into what the binary embeds
	@$(MAKE) --no-print-directory device
	$(ADB) shell rm -rf $(DEVICE_TMP)/echolocal-thumbs
	$(ADB) shell $(ECHOD_DIR)/echod ctl display thumbs $(DEVICE_TMP)/echolocal-thumbs
	rm -f $(CURDIR)/internal/ui/visual/thumbs/*.jpg
	$(ADB) pull $(DEVICE_TMP)/echolocal-thumbs/. $(CURDIR)/internal/ui/visual/thumbs/
	$(ADB) shell rm -rf $(DEVICE_TMP)/echolocal-thumbs

.PHONY: restart-echod
restart-echod: ## Restart echod through init (ctl.stop then ctl.start)
	@eval "$$($(BOARD_SH))"; \
		$(ADB) shell "setprop ctl.stop $$service; sleep 1; setprop ctl.start $$service; \
			sleep 1; echo \"init.svc: \$$(getprop init.svc.$$service)\""

.PHONY: state
state: ## Show what echod and init say about echod
	@eval "$$($(BOARD_SH))"; \
		$(ADB) shell "echo \"state:   \$$(getprop echolocal.state)\"; \
			echo \"started: \$$(getprop echolocal.started)\"; \
			echo \"init.svc: \$$(getprop init.svc.$$service)\""

.PHONY: logs
logs: ## Tail echod logs from a connected device
	$(ADB) logcat -s echolocal:*

.PHONY: shell
shell: ## Open a root shell on a connected device
	$(ADB) shell

##@ Build & Release

AT ?= $(VERSION)
FROM ?= $(RELEASES)/download/$(AT)
PAGE ?= $(RELEASES)/tag/$(AT)

.PHONY: manifest
manifest: build-echod-all ## Build every release binary and write the manifest a device fetches to find this build
	@$(MAKE) --no-print-directory manifest-json

.PHONY: manifest-json
manifest-json: ## Write the manifest for the binaries already in bin
	@mkdir -p $(BUILD_DIR)
	go run ./cmd/mkmanifest \
		-version "$(AT)" \
		-from "$(FROM)" \
		-arm64 $(BUILD_DIR)/echod-arm64 \
		-arm $(BUILD_DIR)/echod-arm \
		$(foreach b,$(BOARD_BUILDS),$(foreach a,$(BOARD_ARCHES),-board $(b):$(a):$(BUILD_DIR)/echod-$(b)-$(a) )) \
		-title "EchoLocal $(AT)" \
		-release-url "$(PAGE)" \
		-out $(BUILD_DIR)/manifest.json
	@cat $(BUILD_DIR)/manifest.json

BOOT_TAG := boot-images
BOOT_DIR := internal/host/assets/boot

LOGO_SRC := assets
LOGO_OUT := internal/brand

.PHONY: logos
logos: ## Rescale the committed artwork into what echod embeds
	@for n in dark light; do \
		go run ./cmd/mklogo -in $(LOGO_SRC)/logo_$$n.png -out $(LOGO_OUT)/logo_$$n.png; \
	done

.PHONY: boot-images
boot-images: ## Publish the boot images the echolocal CLI fetches at install time (run once per board)
	@command -v gh >/dev/null || { echo "needs the gh CLI: brew install gh"; exit 1; }
	@go test ./internal/host/bootimg/ -run TestEveryImageIsInTheTreeAndIsWhatWeSayItIs
	@gh release view $(BOOT_TAG) >/dev/null 2>&1 || \
		gh release create $(BOOT_TAG) --title "Boot images" \
			--notes "Per-board boot images, fetched by the echolocal CLI at install time against a hash compiled into it. Uploaded once per board and never replaced."
	gh release upload $(BOOT_TAG) $(BOOT_DIR)/*.img --clobber
	@echo "$(BOOT_TAG) now serves the boot images"

NEXT_DEV := $(BASE)-dev.$(EPOCH)

.PHONY: release-dev
release-dev: ## Tag this commit as the next dev prerelease; the release workflow builds it
	@$(MAKE) --no-print-directory tag TAG=$(NEXT_DEV)

.PHONY: release-dev-local
release-dev-local: $(UPX) ## Build this working tree here and publish it as the next dev prerelease
	@command -v gh >/dev/null || { echo "needs the gh CLI: brew install gh"; exit 1; }
	@command -v goreleaser >/dev/null || { echo "needs goreleaser: brew install goreleaser"; exit 1; }
	@$(MAKE) --no-print-directory publish-local TAG=$(NEXT_DEV)_local

.PHONY: publish-local
publish-local:
	VERSION=$(TAG) PACK="$(UPX) --lzma -2" goreleaser release --snapshot --clean
	@for f in dist/host_*/echolocal dist/host_*/echolocal.exe; do \
		[ -f "$$f" ] || continue; \
		d=$$(basename $$(dirname $$f)); \
		os=$$(echo $$d | cut -d_ -f2); \
		arch=$$(echo $$d | cut -d_ -f3); \
		if [ "$$arch" = amd64 ]; then arch=x86_64; fi; \
		ext=$${f##*.}; [ "$$ext" = exe ] && ext=.exe || ext=; \
		cp "$$f" "dist/echolocal_$${os}_$${arch}$${ext}"; \
	done
	@$(MAKE) --no-print-directory manifest-json VERSION=$(TAG) FROM=$(RELEASES)/download/$(TAG) PAGE=$(RELEASES)/tag/$(TAG)
	gh release create $(TAG) --prerelease --title $(TAG) \
		--notes "Built locally from $(BUILT_FROM), not by CI." \
		dist/echolocal_* $(BUILD_DIR)/echod-* $(BUILD_DIR)/manifest.json
	@echo "dev channel now serves $(TAG)"

.PHONY: release
release: ## Release the version in VERSION, then start the next patch version
	git push origin HEAD
	@$(MAKE) --no-print-directory tag TAG=$(BASE)
	@next=$$(echo $(BASE) | awk -F. -v OFS=. '{$$NF += 1; print}'); \
		echo "$$next" > VERSION; \
		git commit -q -m "Start $$next" VERSION; \
		echo "VERSION is now $$next (committed, not pushed)"

.PHONY: tag
tag:
	@test -n "$(TAG)" || { echo "usage: make release, make release-dev, make release-dev-local, or make tag TAG=0.4.2"; exit 1; }
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
