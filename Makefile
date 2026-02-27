include Makefile.common

BINARY      := topology_discovery_exporter
BUILD_DIR   := dist
INSTALL_DIR := /usr/local/bin
GO          := go
GOFLAGS     := -trimpath

# Version from git tag, falling back to branch name
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
REVISION    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BRANCH      := $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
BUILD_USER  := $(shell whoami)@$(shell hostname -s)
BUILD_DATE  := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Standard Prometheus version ldflags
VERSION_PKG := github.com/prometheus/common/version
LDFLAGS     := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Revision=$(REVISION) \
	-X $(VERSION_PKG).Branch=$(BRANCH) \
	-X $(VERSION_PKG).BuildUser=$(BUILD_USER) \
	-X $(VERSION_PKG).BuildDate=$(BUILD_DATE)

.PHONY: all build test coverage lint clean install build-linux build-linux-arm64 check-binary version style vet format vulncheck check-all update-deps

all: build

build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

test:
	$(GO) test ./... -v -race -coverprofile=coverage.out

coverage: test
	$(GO) tool cover -html=coverage.out -o coverage.html
	@$(GO) tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

install: build
	install -m 0755 $(BUILD_DIR)/$(BINARY) $(INSTALL_DIR)/$(BINARY)

build-linux:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 .

build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 .

check-binary: build
	@ls -lh $(BUILD_DIR)/$(BINARY)
	@file $(BUILD_DIR)/$(BINARY)
	@$(BUILD_DIR)/$(BINARY) --version

version:
	@echo "Version:    $(VERSION)"
	@echo "Revision:   $(REVISION)"
	@echo "Branch:     $(BRANCH)"

style: common-style
vet: common-vet
format: common-format
vulncheck: common-govulncheck
check-all: common-check
update-deps: common-update-deps

.DEFAULT_GOAL := build
