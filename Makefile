.DEFAULT_GOAL := help

VERSION ?= 0.0.0
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X 'main.Version=$(VERSION)' \
	-X 'main.GitCommit=$(GIT_COMMIT)' \
	-X 'main.BuildDate=$(BUILD_DATE)'

BUILD_DIR := bin
ASSET_DIR := internal/assets/payload

# echod targets the Echo Dot 2: MT8163, Android 5.1 (API 22). Amazon ships a 32-bit userspace but
# the SoC and kernel are arm64 and /system/lib64 is present, so echod is built 64-bit: the wake word
# pipeline costs 50ms per 80ms of audio there against 88ms as 32-bit code, which does not fit.
# The ALSA path is pure Go over /dev/snd ioctls, so no cgo and no NDK image. Keep it that
# way unless something genuinely needs C; the NDK target below exists only for that case.
NDK_IMAGE := echolocal-ndk:latest
DEVICE_ENV := GOOS=linux GOARCH=arm64 CGO_ENABLED=0
DEVICE_LDFLAGS := -s -w $(LDFLAGS)

ADB ?= adb
DEVICE_DIR := /system/etc/echolocal
DEVICE_TMP := /data/local/tmp

# echod lives under /system/app because that tree is labelled u:object_r:system_file:s0,
# the label that leaves an init-started service in init's own domain rather than the narrow
# per-service domain its stock *_exec label would select.
#
# It is installed as Amazon's ledcontroller service: that kills the spinning ring by removing
# its driver, and init then starts echod from on post-fs-data and restarts it if it exits.
ECHOD_DIR := /system/app/echod
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
build-echod: ## Cross-compile echod for the Echo Dot (static, no cgo)
	@mkdir -p $(BUILD_DIR)
	$(DEVICE_ENV) go build -ldflags "$(DEVICE_LDFLAGS)" -o $(BUILD_DIR)/echod ./cmd/echod

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

.PHONY: ndk-image
ndk-image: ## Build the pinned NDK image (only needed if something requires cgo)
	podman build -t $(NDK_IMAGE) -f build/ndk/Dockerfile build/ndk

.PHONY: payload
payload: build-echod ## Stage echod for embedding into echoctl
	@mkdir -p $(ASSET_DIR)
	cp $(BUILD_DIR)/echod $(ASSET_DIR)/echod
	@shasum -a 256 $(ASSET_DIR)/echod | awk '{print $$1}' > $(ASSET_DIR)/echod.sha256

.PHONY: dist
dist: payload build-echoctl ## Full build: echod, payload, then echoctl with it embedded

.PHONY: install-device
install-device: build-echod ## Install echod to /system on a connected device
	$(ADB) push $(BUILD_DIR)/echod $(DEVICE_DIR)/echod

.PHONY: install-echod
install-echod: build-echod ## Install echod into /system/app/echod, restarting it if installed
	@$(ADB) shell 'setprop ctl.stop ledcontroller; sleep 1; \
		mount -o remount,rw /system && mkdir -p $(ECHOD_DIR)'
	@$(ADB) push $(BUILD_DIR)/echod $(ECHOD_DIR)/echod >/dev/null
	@$(ADB) shell 'chmod 755 $(ECHOD_DIR)/echod; mount -o remount,ro /system; \
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

.PHONY: boot-log
boot-log: ## Show echod's boot log from the device
	@$(ADB) shell 'cat /dev/echolocal.log 2>&1; echo "state: $$(getprop echolocal.state)"'

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

.PHONY: snapshot
snapshot: ## Build a local goreleaser snapshot
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) dist/ coverage.out
	rm -f $(ASSET_DIR)/echod $(ASSET_DIR)/echod.sha256
	go clean

##@ Help

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
