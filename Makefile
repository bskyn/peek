BINARY := peek
BIN_DIR := bin
WEB_DIR := web
WEB_NODE_MODULES_STAMP := $(WEB_DIR)/node_modules/.package-lock-stamp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/bskyn/peek/internal/cli.version=$(VERSION)"

.PHONY: install build run test lint clean

$(WEB_NODE_MODULES_STAMP): $(WEB_DIR)/package.json $(WEB_DIR)/package-lock.json
	npm --prefix $(WEB_DIR) install
	touch $(WEB_NODE_MODULES_STAMP)

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

lint: $(WEB_NODE_MODULES_STAMP)
	npm --prefix $(WEB_DIR) run lint
	npm --prefix $(WEB_DIR) run typecheck
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint is required on PATH"; exit 1; }
	golangci-lint run ./cmd/... ./internal/...

clean:
	rm -rf $(BIN_DIR) $(WEB_DIR)/dist
