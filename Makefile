# bamgate Makefile
#
# Targets:
#   build           Build the bamgate CLI binary (default)
#   build-hub       Build the bamgate-hub binary
#   build-all       Build everything (cli + hub + worker + aar)
#   test            Run all Go tests
#   worker          Build the Cloudflare Worker (TinyGo -> Wasm)
#   worker-assets   Copy worker build artifacts into internal/deploy/assets/
#   worker-dev      Start wrangler dev server
#   worker-deploy   Deploy worker to Cloudflare
#   aar             Build Android AAR via gomobile
#   android         Build Android debug APK (depends on aar)
#   install-android Install debug APK to connected device (depends on android)
#   lint            Run golangci-lint
#   fmt             Format all Go code
#   clean           Remove build artifacts

# Overridable variables
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
TINYGO     ?= ~/.local/tinygo/bin/tinygo
GOMOBILE   ?= gomobile
OUTPUT_DIR ?= .

# Derived
LDFLAGS       = -s -w -X main.version=$(VERSION)
LDFLAGS_HUB   = -s -w
WORKER_DIR    = worker
ANDROID_DIR   = android
AAR_OUTPUT    = $(ANDROID_DIR)/app/libs/bamgate.aar
APK_OUTPUT    = $(ANDROID_DIR)/app/build/outputs/apk/debug/app-debug.apk
ASSETS_DIR    = internal/deploy/assets

.PHONY: build build-hub build-all install test e2e worker worker-assets worker-dev worker-deploy \
        aar android install-android lint fmt clean help

# Default target
build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(OUTPUT_DIR)/bamgate ./cmd/bamgate

install: build
	sudo cp $(OUTPUT_DIR)/bamgate /usr/local/bin/bamgate
	sudo chmod 755 /usr/local/bin/bamgate
	@echo "Installed bamgate to /usr/local/bin/bamgate"

build-hub:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS_HUB)' -o $(OUTPUT_DIR)/bamgate-hub ./cmd/bamgate-hub

build-all: build build-hub worker aar

test:
	go test ./...

e2e:
	go test -tags e2e -v -timeout 120s ./test/e2e/

# --- Worker (TinyGo -> Wasm) ---

worker:
	cd $(WORKER_DIR) && $(TINYGO) build -o ./build/app.wasm -target wasm -no-debug .
	cp "$$($(TINYGO) env TINYGOROOT)/targets/wasm_exec.js" $(WORKER_DIR)/build/wasm_exec.js
	cp $(WORKER_DIR)/src/worker.mjs $(WORKER_DIR)/build/worker.mjs

worker-assets: worker
	cp $(WORKER_DIR)/build/app.wasm $(ASSETS_DIR)/app.wasm
	cp $(WORKER_DIR)/build/wasm_exec.js $(ASSETS_DIR)/wasm_exec.js
	cp $(WORKER_DIR)/build/worker.mjs $(ASSETS_DIR)/worker.mjs

worker-dev:
	cd $(WORKER_DIR) && npx wrangler dev

worker-deploy:
	cd $(WORKER_DIR) && npx wrangler deploy

# --- Android ---

aar:
	$(GOMOBILE) bind -target android -androidapi 24 -o $(AAR_OUTPUT) ./mobile/

android: aar
	cd $(ANDROID_DIR) && ./gradlew assembleDebug

install-android: android
	adb install $(APK_OUTPUT)

# --- Code quality ---

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .
	goimports -w .

# --- Cleanup ---

clean:
	rm -f $(OUTPUT_DIR)/bamgate $(OUTPUT_DIR)/bamgate-hub
	rm -rf $(WORKER_DIR)/build/
	rm -f $(AAR_OUTPUT)
	rm -rf $(ANDROID_DIR)/app/build/

# --- Help ---

help:
	@echo "bamgate build targets:"
	@echo ""
	@echo "  build            Build the bamgate CLI binary (default)"
	@echo "  install          Build and install to /usr/local/bin (requires sudo)"
	@echo "  build-hub        Build the bamgate-hub binary"
	@echo "  build-all        Build everything (cli + hub + worker + aar)"
	@echo "  test             Run all Go tests"
	@echo "  e2e              Run Docker e2e tests (3-peer mesh, requires Docker)"
	@echo ""
	@echo "  worker           Build Cloudflare Worker (TinyGo -> Wasm)"
	@echo "  worker-assets    Copy worker artifacts to internal/deploy/assets/"
	@echo "  worker-dev       Start wrangler dev server"
	@echo "  worker-deploy    Deploy worker to Cloudflare"
	@echo ""
	@echo "  aar              Build Android AAR via gomobile"
	@echo "  android          Build Android debug APK (builds AAR first)"
	@echo "  install-android  Install debug APK to connected device"
	@echo ""
	@echo "  lint             Run golangci-lint"
	@echo "  fmt              Format all Go code (gofmt + goimports)"
	@echo "  clean            Remove all build artifacts"
	@echo ""
	@echo "Variables (override with VAR=value):"
	@echo "  VERSION=$(VERSION)"
	@echo "  TINYGO=$(TINYGO)"
	@echo "  GOMOBILE=$(GOMOBILE)"
	@echo "  OUTPUT_DIR=$(OUTPUT_DIR)"
