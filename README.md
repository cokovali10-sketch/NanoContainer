# NanoContainer

**Lightweight Linux Container Runtime with Web Dashboard**

A production-grade container runtime written in Go — implementing the core primitives of Docker/runc from scratch using Linux syscalls. Features a full REST API, real-time web dashboard, compose orchestration, image building, and volume management.

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)
![Linux](https://img.shields.io/badge/Linux-Kernel_5.4+-FCC624?style=flat&logo=linux&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-blue.svg)

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    NanoContainer Platform                       │
├──────────┬──────────┬──────────┬──────────┬─────────────────────┤
│   CLI    │ REST API │ Dashboard│ Compose  │    Image Builder    │
│ (16 cmds)│(16 endpoints)│(Web UI)│(YAML/JSON)│  (Dockerfile)  │
├──────────┴──────────┴──────────┴──────────┴─────────────────────┤
│                      Core Engine                                │
├──────────┬──────────┬──────────┬──────────┬─────────────────────┤
│Namespace │ Cgroups  │ Rootfs   │ Network  │    State Store      │
│ isolation│   v2     │ pivot_root│  veth   │    (JSON)           │
│ PID/Mount│ CPU/Mem/ │ tar.gz   │ bridge  │  Atomic writes      │
│ UTS/Net  │ PIDs     │ /dev     │ NAT     │  Container state    │
├──────────┴──────────┴──────────┴──────────┴─────────────────────┤
│                    Storage Layer                                 │
├──────────┬──────────┬──────────┬──────────┬─────────────────────┤
│Containers│  Images  │ Volumes  │  Logs    │    Metrics          │
│ /var/lib │ .tar.gz  │ named    │ stdout   │  /proc + cgroup    │
│ /nanocont│ registry │ mounts   │ stderr   │  Prometheus         │
└──────────┴──────────┴──────────┴──────────┴─────────────────────┘
```

## Features

### Core
- **Namespace Isolation** — PID, Mount, UTS, Network via `clone()` syscall
- **cgroup v2** — CPU quota, memory limits, PID limits
- **Rootfs** — Image extraction, pivot_root, minimal /dev nodes
- **Network** — Virtual ethernet pairs, bridge networking, NAT, port forwarding
- **State** — Atomic JSON persistence, container lifecycle tracking

### Platform
- **REST API** — 16 endpoints for programmatic container management
- **Web Dashboard** — Real-time dark-themed UI with auto-refresh
- **Compose** — Multi-container orchestration with dependency resolution
- **Image Builder** — Build images from Dockerfiles
- **Volumes** — Named persistent storage
- **Port Forwarding** — iptables DNAT-based port mapping
- **Prometheus Metrics** — System and container metrics endpoint

### CLI
```bash
nanocontainer run <image> <cmd>       # Run container
nanocontainer ps [-a]                 # List containers
nanocontainer stop <id>               # Stop container
nanocontainer logs <id>               # View logs
nanocontainer serve --addr :8080      # Start API + Dashboard
nanocontainer compose up              # Deploy stack
nanocontainer build --file Dockerfile # Build image
nanocontainer volume create <name>    # Create volume
nanocontainer port add <host>:<cont>  # Forward port
```

## Quick Start

### Build

```bash
git clone https://github.com/cokovali10-sketch/NanoContainer.git
cd NanoContainer
GOOS=linux GOARCH=amd64 go build -o nanocontainer ./cmd/nanocontainer
```

### Prepare Image

```bash
# Create image directory
mkdir -p /var/lib/nanocontainer/images

# Download Alpine Linux rootfs
wget -O /var/lib/nanocontainer/images/alpine.tar.gz \
  https://dl-cdn.alpinelinux.org/alpine/v3.19/releases/x86_64/alpine-minirootfs-3.19.1-x86_64.tar.gz

# Or create from Docker
docker export $(docker create alpine:latest) | gzip > /var/lib/nanocontainer/images/alpine.tar.gz
```

### Run

```bash
# Interactive shell
sudo ./nanocontainer run alpine /bin/sh

# Detached with limits
sudo ./nanocontainer run --name web --cpu 50000 --memory 268435456 -d alpine /bin/sh

# Start dashboard
sudo ./nanocontainer serve --addr :8080
# Open http://localhost:8080
```

### Compose

```yaml
# nanocontainer-compose.yml
name: webapp
services:
  nginx:
    image: alpine
    command: ["nginx", "-g", "daemon off;"]
    ports:
      - host: 8080
        container: 80
    depends_on:
      - api
  api:
    image: alpine
    command: ["python3", "-m", "http.server", "5000"]
    ports:
      - host: 5000
        container: 5000
    volumes:
      - source: app-data
        target: /data
    replicas: 2
```

```bash
sudo ./nanocontainer compose up webapp
sudo ./nanocontainer compose ls
sudo ./nanocontainer compose down webapp
```

## REST API

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/health` | Health check |
| GET | `/api/containers` | List containers |
| POST | `/api/containers` | Create container |
| GET | `/api/containers/:id` | Get details |
| POST | `/api/containers/:id/stop` | Stop |
| POST | `/api/containers/:id/start` | Start |
| DELETE | `/api/containers/:id` | Remove |
| GET | `/api/containers/:id/logs` | Get logs |
| GET | `/api/images` | List images |
| POST | `/api/images/pull` | Pull image |
| POST | `/api/images/build` | Build image |
| GET | `/api/stats` | System stats |
| GET | `/api/metrics` | Prometheus metrics |
| GET | `/api/compose` | List stacks |
| POST | `/api/compose` | Deploy stack |
| DELETE | `/api/compose/:name` | Remove stack |

## Project Structure

```
NanoContainer/
├── cmd/nanocontainer/
│   └── main.go              # CLI entry point (16 commands)
├── internal/
│   ├── api/
│   │   ├── server.go        # REST API server (16 endpoints)
│   │   ├── middleware.go    # CORS, logging, recovery, rate limit
│   │   ├── stats.go         # System stats + Prometheus metrics
│   │   ├── dashboard.go     # Web dashboard server
│   │   └── dashboard.html   # Full dark-themed Web UI
│   ├── cli/
│   │   ├── colors.go        # ANSI color helpers
│   │   └── progress.go      # Progress bar
│   ├── compose/
│   │   └── compose.go       # Multi-container orchestration
│   ├── config/
│   │   └── config.go        # Configuration management
│   ├── container/
│   │   ├── container.go     # Container lifecycle
│   │   └── init.go          # Namespace init process (PID 1)
│   ├── cgroup/
│   │   └── cgroup.go        # cgroup v2 management
│   ├── image/
│   │   └── builder.go       # Dockerfile parser + image builder
│   ├── namespace/
│   │   └── namespace.go     # Linux namespace operations
│   ├── network/
│   │   ├── network.go       # Bridge + veth networking
│   │   └── portforward.go   # Port forwarding via iptables
│   ├── rootfs/
│   │   └── rootfs.go        # Rootfs/image management
│   ├── store/
│   │   └── store.go         # State persistence
│   └── volume/
│       └── volume.go        # Named volume management
├── nanocontainer-compose.yml
├── Dockerfile.example
├── go.mod
├── go.sum
└── README.md
```

## Requirements

- Linux kernel 5.4+ (cgroup v2)
- Root privileges
- Go 1.22+

## Security

- Containers run in isolated namespaces
- cgroup resource limits prevent resource exhaustion
- Rate limiting on API endpoints
- Input validation on all API parameters
- Atomic state writes prevent corruption

## License

MIT
