# tabby-sync top-level Makefile
#
# Conventions:
#   - Standard library Go only; no third-party tooling is auto-installed here.
#   - `govulncheck` (used by the `vuln` target) must already be on PATH; the
#     repository keeps it at /root/go/bin/govulncheck in CI/dev sandboxes, so
#     callers typically invoke `PATH=$$PATH:/root/go/bin make vuln`.
#   - All recipe lines are tab-indented (Makefile requirement).

SHELL := /bin/sh

# Module path for -ldflags -X targets.
MODULE  := github.com/kuddy-ai/tabby-sync

# Build metadata (override on the command line as needed, e.g.
# `make build VERSION=1.2.3`).
VERSION ?= 0.0.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# -ldflags injected into the binary so `tabby-sync version` reports build info.
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

# Output paths.
BIN_DIR := bin
BIN     := $(BIN_DIR)/tabby-sync

# Extra arguments forwarded to `make run` (e.g. `make run ARGS=version`).
ARGS ?=

.DEFAULT_GOAL := help

.PHONY: help build test vet fmt fmt-check vuln run clean

help: ## Show this help.
	@echo "tabby-sync make targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build ./bin/tabby-sync with version metadata baked in.
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/tabby-sync

test: ## Run unit tests with the race detector.
	go test -race -count=1 ./...

vet: ## Run `go vet` across all packages.
	go vet ./...

fmt: ## Rewrite source files with `gofmt -s -w`.
	gofmt -s -w .

fmt-check: ## Fail if `gofmt -s -l .` reports any files.
	@out=$$(gofmt -s -l .); \
	if [ -n "$$out" ]; then \
		echo "gofmt -s -l found unformatted files:"; \
		echo "$$out"; \
		exit 1; \
	fi

vuln: ## Run govulncheck (must be installed and on PATH).
	govulncheck ./...

run: ## Run the binary from source: `make run ARGS=version`.
	go run ./cmd/tabby-sync $(ARGS)

clean: ## Remove build outputs.
	rm -rf $(BIN_DIR)
