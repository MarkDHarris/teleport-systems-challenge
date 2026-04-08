# Reproducible builds: use consistent toolchain; trimpath strips local paths from binaries.
GO       ?= go
GOFLAGS  ?= -trimpath

BIN_DIR     := bin
SERVER_BIN  := $(BIN_DIR)/server
CLIENT_BIN  := $(BIN_DIR)/jobctl

GO_TEST_FLAGS := -race -count=1

GOLANGCI_LINT_VERSION := v1.63.0

COVERAGE_FILE := coverage.out
COVERAGE_HTML := coverage.html

# Application code only (skip generated api/v1 and untested cmd/server main).
COVERPKG := ./internal/...,./cmd/jobctl

.PHONY: all help build certs test test-coverage coverage-html clean fmt fmt-check lint check proto install-tools

# Default: show available targets
all: help

help:
	@echo "JobWorkerService — Makefile targets"
	@echo ""
	@echo "  make build           Build $(SERVER_BIN) and $(CLIENT_BIN)"
	@echo "  make test            Run all tests ($(GO_TEST_FLAGS), verbose)"
	@echo "  make test-coverage   Tests + coverage.out + go tool cover -func ($(COVERPKG))"
	@echo "  make coverage-html   HTML coverage report ($(COVERAGE_HTML))"
	@echo "  make lint            gofmt check + golangci-lint"
	@echo "  make check           fmt-check + lint + test (merge-style gate)"
	@echo "  make fmt             Apply gofmt to all packages"
	@echo "  make proto           Regenerate api/v1/*.pb.go from worker.proto"
	@echo "  make certs           Generate mTLS PEMs under scripts/certs/ (OpenSSL; not in git)"
	@echo "  make clean           Remove coverage artifacts and $(BIN_DIR)/"
	@echo "  make install-tools   Install pinned golangci-lint ($(GOLANGCI_LINT_VERSION))"
	@echo ""
	@echo "Variables: GO=$(GO) GOFLAGS=$(GOFLAGS)"

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(SERVER_BIN) ./cmd/server
	$(GO) build $(GOFLAGS) -o $(CLIENT_BIN) ./cmd/jobctl

# Test/dev mTLS PEMs (see .gitignore); required before first local run or integration tests.
certs:
	@command -v openssl >/dev/null 2>&1 || { echo "error: openssl not found (install OpenSSL and retry)"; exit 1; }
	bash scripts/generate-certs.sh

proto:
	protoc -I api/v1 \
		--go_out=api/v1 --go_opt=paths=source_relative \
		--go-grpc_out=api/v1 --go-grpc_opt=paths=source_relative \
		api/v1/worker.proto

test:
	$(GO) test $(GO_TEST_FLAGS) -v ./...

test-coverage:
	$(GO) test $(GO_TEST_FLAGS) -coverprofile=$(COVERAGE_FILE) -coverpkg=$(COVERPKG) ./...
	$(GO) tool cover -func=$(COVERAGE_FILE)

coverage-html: test-coverage
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Open $(COVERAGE_HTML) in your browser"

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Run: make fmt"; gofmt -l .; exit 1)

lint: fmt-check
	golangci-lint run ./...

# lint already depends on fmt-check
check: lint test

clean:
	rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)
	rm -rf $(BIN_DIR)

install-tools:
	@echo "Installing golangci-lint..."
	@$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
