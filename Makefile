.PHONY: all build test test-unit test-integration clean install help

# Default target
all: build

# Build both binaries
build:
	@echo "Building psync binaries..."
	@go build -o psync ./cmd/psync
	@go build -o psyncd ./cmd/psyncd
	@echo "Build complete: psync, psyncd"

# Build client only
build-client:
	@go build -o psync ./cmd/psync

# Build server only
build-server:
	@go build -o psyncd ./cmd/psyncd

# Run all tests
test: test-unit test-integration

# Run unit tests
test-unit:
	@echo "Running unit tests..."
	@go test -v ./...

# Run unit tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@go test -cover ./...
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run unit tests with race detection
test-race:
	@echo "Running tests with race detector..."
	@go test -race ./...

# Run integration tests
test-integration: build
	@echo "Running integration tests..."
	@./test-integration.sh

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Vet code
vet:
	@echo "Vetting code..."
	@go vet ./...

# Run all checks (fmt, vet, test)
check: fmt vet test-unit
	@echo "All checks passed!"

# Generate type string methods
generate:
	@echo "Generating type string methods..."
	@go generate ./...

# Clean build artifacts and test files
clean:
	@echo "Cleaning..."
	@rm -f psync psyncd
	@rm -f coverage.out coverage.html
	@rm -rf /tmp/psync-test-*
	@echo "Clean complete"

# Install binaries to $GOPATH/bin
install:
	@echo "Installing binaries..."
	@go install ./cmd/psync
	@go install ./cmd/psyncd
	@echo "Installed to $(shell go env GOPATH)/bin"

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	@go mod download
	@go mod tidy

# Help target
help:
	@echo "Available targets:"
	@echo "  make build              - Build both psync and psyncd binaries"
	@echo "  make build-client       - Build only psync client"
	@echo "  make build-server       - Build only psyncd server"
	@echo "  make test               - Run all tests (unit + integration)"
	@echo "  make test-unit          - Run unit tests"
	@echo "  make test-integration   - Run integration test suite"
	@echo "  make test-coverage      - Run tests with coverage report"
	@echo "  make test-race          - Run tests with race detector"
	@echo "  make fmt                - Format code with gofmt"
	@echo "  make vet                - Run go vet"
	@echo "  make check              - Run fmt, vet, and unit tests"
	@echo "  make generate           - Generate type string methods"
	@echo "  make clean              - Remove build artifacts"
	@echo "  make install            - Install binaries to GOPATH/bin"
	@echo "  make deps               - Download and tidy dependencies"
	@echo "  make help               - Show this help message"
