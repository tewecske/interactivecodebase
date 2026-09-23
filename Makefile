BINARY_NAME := icb
GO := go
GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tewecske/interactivecodebase/internal/cli.Version=$(VERSION)

.PHONY: build clean fmt-check vet test test-libs test-goweb lint ui check

build: ui
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) ./cmd/icb

clean:
	rm -rf bin coverage.out

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

test:
	# -p 2: packages analyze whole modules under -race; running them all at
	# once can exhaust memory on a shared machine.
	$(GO) test -p 2 -race -shuffle=on ./...

# Checks the router and data-access library fixtures. Not under -race, which
# triples the memory of each analysis (the test file is excluded from race
# builds).
test-libs:
	$(GO) test -run 'RouterFrameworks|DataAccessLibraries' ./internal/analysis

# Checks the goweb golden expectations against a goweb checkout at the pinned
# commit: ../goweb by default, or ICB_GOWEB_DIR.
test-goweb:
	$(GO) test -p 2 -tags goweb -run Goweb -v ./...

lint:
	$(GOLANGCI_LINT) run ./...

# Builds the web UI (Node 22+) into internal/webui/dist, which the binary
# embeds. Without it the binary still works and serves a notice at "/".
ui:
	cd ui && npm ci && npm test && npm run build

check: fmt-check vet lint test test-libs
