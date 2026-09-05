# Contributing to NanoContainer

Thank you for your interest in contributing! This guide will help you get started.

## Prerequisites

- Go 1.22+
- Linux (for testing)
- Root access (for running containers)
- Git

## Development Setup

```bash
# Clone the repository
git clone https://github.com/cokovali10-sketch/NanoContainer.git
cd NanoContainer

# Build
GOOS=linux GOARCH=amd64 go build -o nanocontainer ./cmd/nanocontainer

# Run tests
go test -v ./...

# Run integration tests (requires root)
sudo go test -v -run Integration ./...
```

## Project Structure

```
NanoContainer/
├── cmd/nanocontainer/       # CLI entry point
├── internal/
│   ├── api/                 # REST API server
│   ├── auth/                # Authentication
│   ├── cli/                 # CLI helpers (colors, progress)
│   ├── compose/             # Compose orchestration
│   ├── config/              # Configuration
│   ├── container/           # Container lifecycle
│   ├── copy/                # File copy
│   ├── cgroup/              # cgroup v2
│   ├── exec/                # Exec into containers
│   ├── health/              # Health checks
│   ├── image/               # Image builder
│   ├── inspect/             # Container inspection
│   ├── namespace/           # Linux namespaces
│   ├── network/             # Networking
│   ├── orchestration/       # Service management
│   ├── registry/            # Image registry
│   ├── rootfs/              # Rootfs management
│   ├── secrets/             # Secrets management
│   ├── security/            # Security (seccomp, caps)
│   ├── stats/               # Statistics
│   ├── storage/             # OverlayFS
│   ├── store/               # State persistence
│   ├── volume/              # Volume management
│   └── websocket/           # WebSocket
├── tests/                   # Integration tests
└── docs/                    # Documentation
```

## Code Style

- Use `gofmt` for formatting
- Use `go vet` for static analysis
- Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
- Add build tags `//go:build linux` for Linux-specific code
- Handle errors with `fmt.Errorf("context: %w", err)`

## Commit Messages

Use conventional commits:
```
feat: add new feature
fix: bug fix
docs: documentation
test: add tests
refactor: code refactoring
perf: performance improvement
```

## Pull Request Process

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Run `go test -v ./...`
6. Submit a pull request

## Reporting Issues

Use GitHub Issues with:
- Clear title
- Steps to reproduce
- Expected behavior
- Actual behavior
- Go version and OS

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
