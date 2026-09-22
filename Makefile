BINARY_NAME := icb
GO := go
GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tewecske/interactivecodebase/internal/cli.Version=$(VERSION)

.PHONY: build clean fmt-check vet test lint ui check

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
	$(GO) test -race -shuffle=on ./...

lint:
	$(GOLANGCI_LINT) run ./...

# The web UI lands in ui/ with #14; until then this is a no-op.
ui:
	@if [ -f ui/package.json ]; then cd ui && npm ci && npm run build; else echo "ui: no ui/package.json, skipping"; fi

check: fmt-check vet lint test
