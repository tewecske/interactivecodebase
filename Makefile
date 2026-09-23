BINARY_NAME := icb
GO := go
GOLANGCI_LINT_VERSION := v2.13.2
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/tewecske/interactivecodebase/internal/cli.Version=$(VERSION)

.PHONY: build clean fmt-check vet test test-libs test-goweb test-scala test-gathedge scala-extractor lint ui check

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

# Checks the router, data-access and entry-point fixtures. Not under -race, which
# triples the memory of each analysis (the test file is excluded from race
# builds).
test-libs:
	$(GO) test -run 'RouterFrameworks|DataAccessLibraries|EntryPointsFixture' ./internal/analysis

# Checks the goweb golden expectations against a goweb checkout at the pinned
# commit: ../goweb by default, or ICB_GOWEB_DIR.
test-goweb:
	$(GO) test -p 2 -tags goweb -run Goweb -v ./...

# The Scala extractor (extractors/scala) and the sbt it is built with run on
# the JVM; heaps are capped because the machine may be shared.
SBT := sbt -batch -no-colors -J-Xmx1500m

# Builds the Scala extractor and writes extractors/scala/target/icb-scala,
# the command icb runs for sbt projects (see ICB_SCALA in the README).
scala-extractor:
	cd extractors/scala && $(SBT) launcher

# Tests the extractor, then analyzes testdata/fixtures/scala/zioapp through
# sbt and the extractor. Skipped without sbt and java.
test-scala:
	@if ! command -v sbt >/dev/null || ! command -v java >/dev/null; then \
		echo "test-scala: sbt or java not installed, skipped"; \
	else \
		set -e; \
		(cd extractors/scala && $(SBT) test launcher); \
		ICB_SCALA=$(CURDIR)/extractors/scala/target/icb-scala $(GO) test -count=1 -run 'ScalaFixture' ./internal/cli; \
	fi

# Checks testdata/golden/gathedge.json against a gathedge checkout at the
# pinned commit: ../gathedge next to this repository or its parent
# directory, or ICB_GATHEDGE_DIR. sbt compiles it, writing only its target
# directories. ICB_GATHEDGE_UPDATE=1 rewrites the golden instead.
test-gathedge: scala-extractor
	ICB_SCALA=$(CURDIR)/extractors/scala/target/icb-scala $(GO) test -count=1 -tags gathedge -run Gathedge -v ./internal/cli

lint:
	$(GOLANGCI_LINT) run ./...

# Builds the web UI (Node 22+) into internal/webui/dist, which the binary
# embeds. Without it the binary still works and serves a notice at "/".
ui:
	cd ui && npm ci && npm test && npm run build

check: fmt-check vet lint test test-libs test-scala
