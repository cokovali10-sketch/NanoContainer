.PHONY: build test clean install dev demo docker release

# NanoContainer Makefile

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

# Build targets
build:
	@echo "Building nanocontainer..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o nanocontainer ./cmd/nanocontainer
	@echo "Build complete: ./nanocontainer"

build-arm64:
	@echo "Building nanocontainer (arm64)..."
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o nanocontainer-arm64 ./cmd/nanocontainer

build-all: build build-arm64

# Development
dev:
	@echo "Starting in development mode..."
	go run ./cmd/nanocontainer serve --addr :8080

test:
	@echo "Running tests..."
	go test -v -short ./...

test-integration:
	@echo "Running integration tests (requires root)..."
	sudo go test -v -run Integration ./...

bench:
	@echo "Running benchmarks..."
	go test -bench=. -benchmem ./...

# Code quality
lint:
	@echo "Running linter..."
	golangci-lint run ./...

fmt:
	@echo "Formatting code..."
	gofmt -s -w .

vet:
	@echo "Running go vet..."
	go vet ./...

# Install
install:
	@echo "Installing nanocontainer..."
	go install -ldflags "$(LDFLAGS)" ./cmd/nanocontainer
	@echo "Installed to $(GOPATH)/bin/nanocontainer"

uninstall:
	@echo "Removing nanocontainer..."
	rm -f $(GOPATH)/bin/nanocontainer

# Docker
docker:
	@echo "Building Docker image..."
	docker build -t nanocontainer:$(VERSION) .

docker-run:
	@echo "Running in Docker..."
	docker run --privileged -p 8080:8080 nanocontainer:$(VERSION)

# Demo
demo:
	@echo "Starting NanoContainer demo..."
	@./scripts/demo.sh

quickstart:
	@echo "Running quickstart..."
	@curl -fsSL https://raw.githubusercontent.com/cokovali10-sketch/NanoContainer/master/scripts/install.sh | bash

# Release
release:
	@echo "Building release..."
	goreleaser release --clean

snapshot:
	@echo "Building snapshot..."
	goreleaser release --snapshot --clean

# Clean
clean:
	@echo "Cleaning..."
	rm -rf nanocontainer nanocontainer-arm64 dist/
	go clean -cache

# Help
help:
	@echo ""
	@echo "NanoContainer - Lightweight Linux Container Runtime"
	@echo ""
	@echo "Usage:"
	@echo "  make build          Build for linux/amd64"
	@echo "  make build-arm64    Build for linux/arm64"
	@echo "  make build-all      Build for all architectures"
	@echo "  make dev            Start in development mode"
	@echo "  make test           Run unit tests"
	@echo "  make test-integration  Run integration tests (root)"
	@echo "  make bench          Run benchmarks"
	@echo "  make install        Install to GOPATH/bin"
	@echo "  make docker         Build Docker image"
	@echo "  make demo           Run interactive demo"
	@echo "  make quickstart     One-line install"
	@echo "  make release        Build release with goreleaser"
	@echo "  make clean          Clean build artifacts"
	@echo ""
