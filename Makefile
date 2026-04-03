GO_TEST_FLAGS := -race -count=1

GOLANGCI_LINT_VERSION := v1.63.0

COVERAGE_FILE := coverage.out
COVERAGE_HTML := coverage.html

.PHONY: test test-coverage coverage-html clean fmt fmt-check lint install-tools

test:
	go test $(GO_TEST_FLAGS) -v ./...

test-coverage:
	go test $(GO_TEST_FLAGS) -coverprofile=$(COVERAGE_FILE) ./...
	go tool cover -func=$(COVERAGE_FILE)

coverage-html: test-coverage
	go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Open $(COVERAGE_HTML) in your browser"

fmt:
	go fmt ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Run: make fmt"; gofmt -l .; exit 1)

lint: fmt-check
	golangci-lint run ./...

clean:
	rm -f $(COVERAGE_FILE) $(COVERAGE_HTML)

install-tools:
	@echo "Installing golangci-lint..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
