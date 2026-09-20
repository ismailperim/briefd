BINARY  := briefd
MODULE  := github.com/ismailperim/briefd
BIN_DIR := bin

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: all build test lint fmt tidy eval clean help

all: build test lint ## Build, test and lint

build: ## Build the binary into bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/briefd

test: ## Run tests with the race detector
	go test -race -cover ./...

lint: ## Run golangci-lint (includes formatter checks)
	golangci-lint run ./...

fmt: ## Format sources with the configured formatters
	golangci-lint fmt ./...

eval: build ## Run the retrieval quality eval against the golden set (downloads the model once)
	./$(BIN_DIR)/$(BINARY) eval --config /dev/null

tidy: ## Tidy go.mod/go.sum and fail if anything changed
	go mod tidy
	git diff --exit-code go.mod go.sum

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) dist coverage.out

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'
