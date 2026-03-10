BINARY := peek
BIN_DIR := bin
WEB_DIR := web
WEB_NODE_MODULES_STAMP := $(WEB_DIR)/node_modules/.package-lock-stamp
GOLANGCI_LINT_VERSION_FILE := .golangci-lint-version
GOLANGCI_LINT_VERSION := $(shell cat $(GOLANGCI_LINT_VERSION_FILE))
GOLANGCI_LINT := $(BIN_DIR)/golangci-lint
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/bskyn/peek/internal/cli.version=$(VERSION)"

.PHONY: install build run test lint clean

$(WEB_NODE_MODULES_STAMP): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	npm --prefix $(WEB_DIR) install
	touch $(WEB_NODE_MODULES_STAMP)

$(GOLANGCI_LINT): $(GOLANGCI_LINT_VERSION_FILE)
	mkdir -p $(BIN_DIR)
	bash -o pipefail -c 'curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(BIN_DIR) $(GOLANGCI_LINT_VERSION)'

install: $(WEB_NODE_MODULES_STAMP)

build: $(WEB_NODE_MODULES_STAMP)
	npm --prefix $(WEB_DIR) run build
	mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/peek

run: build
	./$(BIN_DIR)/$(BINARY) $(ARGS)

test: $(WEB_NODE_MODULES_STAMP)
	npm --prefix $(WEB_DIR) run build
	go test -race ./cmd/... ./internal/...

lint: $(WEB_NODE_MODULES_STAMP) $(GOLANGCI_LINT)
	npm --prefix $(WEB_DIR) run lint
	npm --prefix $(WEB_DIR) run typecheck
	$(GOLANGCI_LINT) run ./cmd/... ./internal/...

clean:
	rm -rf $(BIN_DIR) $(WEB_DIR)/dist
