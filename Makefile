.DEFAULT_GOAL := help

VERSION ?= 0.0.0
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X 'main.Version=$(VERSION)' \
	-X 'main.GitCommit=$(GIT_COMMIT)' \
	-X 'main.BuildDate=$(BUILD_DATE)'

BUILD_DIR := bin
ASSET_DIR := internal/assets/payload

# echod targets the Echo Dot 2: MT8163, 32-bit userspace, Android 5.1 (API 22).
# cgo is required for speexdsp, so it builds in the pinned NDK image.
NDK_IMAGE := echolocal-ndk:latest
DEVICE_ENV := GOOS=android GOARCH=arm GOARM=7 CGO_ENABLED=1
DEVICE_LDFLAGS := -s -w $(LDFLAGS)

ADB ?= adb
DEVICE_DIR := /system/etc/echolocal

##@ Development

.PHONY: build
build: echoctl ## Build echoctl for the host

.PHONY: echoctl
echoctl: ## Build the host CLI into ./bin
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/echoctl ./cmd/echoctl

.PHONY: run
run: ## Run echoctl directly (e.g. make run ARGS="status")
	go run ./cmd/echoctl $(ARGS)

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
ndk-image: ## Build the pinned NDK cross-compile image
	podman build -t $(NDK_IMAGE) -f build/ndk/Dockerfile build/ndk

.PHONY: echod
echod: ndk-image ## Cross-compile echod for the Echo Dot (android/arm)
	@mkdir -p $(BUILD_DIR)
	podman run --rm -v $(PWD):/src -w /src $(NDK_IMAGE) \
		sh -c '$(DEVICE_ENV) go build -ldflags "$(DEVICE_LDFLAGS)" -o $(BUILD_DIR)/echod ./cmd/echod'

.PHONY: payload
payload: echod ## Stage echod and assets for embedding into echoctl
	@mkdir -p $(ASSET_DIR)
	cp $(BUILD_DIR)/echod $(ASSET_DIR)/echod
	@shasum -a 256 $(ASSET_DIR)/echod | awk '{print $$1}' > $(ASSET_DIR)/echod.sha256

.PHONY: dist
dist: payload echoctl ## Full build: echod, payload, then echoctl with it embedded

.PHONY: push
push: echod ## Push echod to a connected device without a full install
	$(ADB) push $(BUILD_DIR)/echod $(DEVICE_DIR)/echod

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
