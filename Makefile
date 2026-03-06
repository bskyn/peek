BINARY := peek
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/bskyn/peek/internal/cli.version=$(VERSION)"

.PHONY: build test lint run clean release-dry-run

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/peek

test:
	go test -race ./...

lint:
	golangci-lint run

run:
	go run $(LDFLAGS) ./cmd/peek $(ARGS)

clean:
	rm -rf bin/ dist/

release-dry-run:
	goreleaser release --snapshot --skip=publish --clean
