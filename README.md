# JobWorkerService

A secure Go-based job worker service for executing and managing Linux processes via API and CLI.

This repository provides a remote Linux job execution library with features to start processes
without shell interpretation, stream stdout/stderr from the remote process, and manage lifecycle (status, cancel).

## Requirements

- [Go](https://go.dev/dl/) 1.26 or newer (see `go.mod`)

## Quick start

```bash
git clone https://github.com/MarkDHarris/JobWorkerService.git
cd JobWorkerService
make test
```

## Make targets

| Target | Description |
|--------|-------------|
| `make test` | Run all tests with `-race`, `-count=1`, and verbose output |
| `make test-coverage` | Run all tests with `-race`, `-count=1`, coverage profile (`coverage.out`) and `go tool cover -func` summary |
| `make coverage-html` | HTML report (`coverage.html`; depends on `test-coverage`) |
| `make fmt` | Run `go fmt ./...` (rewrite) |
| `make fmt-check` | Fail if any file needs `gofmt` (read-only) |
| `make lint` | Run `fmt-check`, then `golangci-lint run ./...` |
| `make clean` | Remove artifacts |
| `make install-tools` | Install the Makefile’s pinned `golangci-lint` version |

## License

See [LICENSE](LICENSE).
