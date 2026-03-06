BINARY := peek
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/bskyn/peek/internal/cli.version=$(VERSION)"
GOBIN ?= $(CURDIR)/bin
GOLANGCI_LINT_VERSION := v1.64.8
GOLANGCI_LINT := $(GOBIN)/golangci-lint
GOLANGCI_LINT_VERSION_STRIPPED := $(patsubst v%,%,$(GOLANGCI_LINT_VERSION))
UNAME_S := $(shell uname -s)
UNAME_M := $(shell uname -m)

ifeq ($(UNAME_S),Darwin)
GOLANGCI_LINT_OS := darwin
else ifeq ($(UNAME_S),Linux)
GOLANGCI_LINT_OS := linux
else
$(error unsupported OS $(UNAME_S) for golangci-lint install)
endif

ifeq ($(UNAME_M),x86_64)
GOLANGCI_LINT_ARCH := amd64
else ifeq ($(UNAME_M),arm64)
GOLANGCI_LINT_ARCH := arm64
else ifeq ($(UNAME_M),aarch64)
GOLANGCI_LINT_ARCH := arm64
else
$(error unsupported ARCH $(UNAME_M) for golangci-lint install)
endif

.PHONY: build test lint lint-install run clean release-dry-run

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/peek

test:
	go test -race ./...

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./cmd/... ./internal/...

lint-install: $(GOLANGCI_LINT)

$(GOLANGCI_LINT):
	@mkdir -p $(GOBIN)
	@tmpdir=$$(mktemp -d); \
	archive="golangci-lint-$(GOLANGCI_LINT_VERSION_STRIPPED)-$(GOLANGCI_LINT_OS)-$(GOLANGCI_LINT_ARCH).tar.gz"; \
	url="https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/$$archive"; \
	echo "Downloading $$url"; \
	curl -fsSL "$$url" -o "$$tmpdir/$$archive"; \
	tar -xzf "$$tmpdir/$$archive" -C "$$tmpdir"; \
	cp "$$tmpdir/golangci-lint-$(GOLANGCI_LINT_VERSION_STRIPPED)-$(GOLANGCI_LINT_OS)-$(GOLANGCI_LINT_ARCH)/golangci-lint" "$(GOLANGCI_LINT)"; \
	chmod +x "$(GOLANGCI_LINT)"; \
	rm -rf "$$tmpdir"

run:
	go run $(LDFLAGS) ./cmd/peek $(ARGS)

clean:
	rm -rf bin/ dist/

release-dry-run:
	goreleaser release --snapshot --skip=publish --clean
