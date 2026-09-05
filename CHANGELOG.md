# Changelog

All notable changes to NanoContainer will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [3.0.0] - 2026-09-05

### Added
- **Security**
  - Seccomp BPF filters (50+ blocked syscalls)
  - Linux capabilities (32 caps, ambient capabilities)
  - Read-only rootfs support
  - No-new-privileges flag
  - Encrypted secrets (AES-256-GCM)
- **Developer Experience**
  - Exec into running containers (nsenter)
  - Copy files to/from containers (tar streaming)
  - Real-time container stats (CPU, memory, network, disk)
  - Full container inspection (docker inspect equivalent)
- **Health & Restart**
  - HTTP/TCP/Command health checks
  - Restart policies (always, on-failure, unless-stopped)
- **Real-time**
  - WebSocket event streaming
  - Hub pattern for client management
- **Authentication**
  - JWT-like token authentication
  - API key management
  - RBAC middleware
- **Storage**
  - OverlayFS layered filesystem
  - Volume backup/restore
- **Networking**
  - DNS service discovery
  - Isolated networks
  - Port forwarding (iptables DNAT)
- **Orchestration**
  - Service manager with rolling updates
  - Load balancing (random healthy replica)
  - BinPack and Spread scheduling
- **Registry**
  - Local image registry
  - Push/pull/tag operations
  - Docker v2 API compatible
- **Documentation**
  - Enterprise-grade README
  - Architecture diagrams (6 ASCII diagrams)
  - CONTRIBUTING.md
  - ARCHITECTURE.md
  - OpenAPI 3.0 specification
  - Dashboard mockup HTML
- **CI/CD**
  - GitHub Actions (build + test + release)
  - Multi-architecture builds (amd64/arm64)
  - Goreleaser configuration

## [2.0.0] - 2026-09-05

### Added
- REST API server (16 endpoints)
- Web Dashboard (dark-themed, real-time)
- Compose orchestration (YAML/JSON)
- Image Builder (Dockerfile parser)
- Volume management
- Port forwarding
- Prometheus metrics
- Enhanced CLI (16 commands)

## [1.0.0] - 2026-09-05

### Added
- Initial release
- Namespace isolation (PID, Mount, UTS, Network)
- cgroup v2 (CPU, Memory, PIDs)
- Rootfs management
- Bridge networking
- CLI interface (run, ps, stop, logs)
- State persistence

[Unreleased]: https://github.com/cokovali10-sketch/NanoContainer/compare/v3.0.0...HEAD
[3.0.0]: https://github.com/cokovali10-sketch/NanoContainer/compare/v2.0.0...v3.0.0
[2.0.0]: https://github.com/cokovali10-sketch/NanoContainer/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/cokovali10-sketch/NanoContainer/releases/tag/v1.0.0
