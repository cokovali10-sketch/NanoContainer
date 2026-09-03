# NanoContainer

Lightweight Linux container runtime written in Go. A minimal Docker/runc alternative that implements core container primitives directly using Linux syscalls.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      CLI Layer                          │
│  nanocontainer run | ps | stop | logs | pull | images   │
├─────────────────────────────────────────────────────────┤
│                   Container Manager                     │
│  Create → Start → Attach → Stop → Cleanup               │
├──────────┬──────────┬──────────┬──────────┬─────────────┤
│Namespace │ Cgroups  │ Rootfs   │ Network  │  State      │
│ (clone)  │ (v2)     │ (pivot)  │ (veth)   │  (JSON)     │
│          │          │          │          │             │
│ PID      │ CPU      │ tar.gz   │ Bridge   │ containers/ │
│ Mount    │ Memory   │ devnodes │ NAT      │ images/     │
│ UTS      │ PIDs     │ procfs   │ IP alloc │ network/    │
│ Net      │          │ resolv   │          │             │
└──────────┴──────────┴──────────┴──────────┴─────────────┘
         │                │                │
    Linux Kernel    Linux Kernel     Linux Kernel
  (clone syscall)  (cgroup fs)     (netlink/ioctl)
```

## Features

- **Namespace Isolation**: PID, Mount, UTS, Network namespaces via `clone()` syscall
- **Resource Limits**: CPU quota, memory limits, PID limits via cgroup v2
- **Rootfs Management**: Image extraction, pivot_root, minimal /dev nodes
- **Network**: Virtual ethernet pairs, bridge networking, NAT
- **State Management**: Atomic JSON state files, container lifecycle tracking
- **CLI**: Full command-line interface with run, ps, stop, logs, pull, images

## Prerequisites

- Linux kernel 5.4+ (cgroup v2)
- Root privileges (required for namespaces/cgroups)
- Go 1.22+

## Build

```bash
# Clone the repository
git clone https://github.com/cokovali10-sketch/NanoContainer.git
cd NanoContainer

# Build
go build -o nanocontainer ./cmd/nanocontainer

# Or install
go install ./cmd/nanocontainer
```

## Usage

### Pull an image

```bash
# Create a minimal rootfs tarball (Alpine Linux)
# Option 1: From Docker
docker export $(docker create alpine:latest) | gzip > images/alpine.tar.gz

# Option 2: Manual creation
mkdir rootfs && sudo debootstrap --variant=minbase bullseye rootfs
tar -czf images/alpine.tar.gz -C rootfs .

# Place in NanoContainer's image directory
mkdir -p /var/lib/nanocontainer/images
cp alpine.tar.gz /var/lib/nanocontainer/images/
```

### Run a container

```bash
# Interactive shell (foreground)
sudo ./nanocontainer run alpine /bin/sh

# Detached with resource limits
sudo ./nanocontainer run --name web --cpu 50000 --memory 268435456 -d alpine /bin/sh

# Run a specific command
sudo ./nanocontainer run alpine echo "Hello from container"
```

### Manage containers

```bash
# List running containers
sudo ./nanocontainer ps

# List all containers (including stopped)
sudo ./nanocontainer ps -a

# Show container logs
sudo ./nanocontainer logs <container-id>

# Stop a container
sudo ./nanocontainer stop <container-id>
```

### Image management

```bash
# List available images
sudo ./nanocontainer images

# Pull image from URL
sudo ./nanocontainer pull alpine https://dl-cdn.alpinelinux.org/alpine/v3.19/releases/x86_64/alpine-minirootfs-3.19.1-x86_64.tar.gz
```

## Project Structure

```
NanoContainer/
├── cmd/nanocontainer/
│   └── main.go              # CLI entry point
├── internal/
│   ├── config/
│   │   └── config.go        # Configuration management
│   ├── container/
│   │   ├── container.go     # Container lifecycle
│   │   └── init.go          # Namespace init process
│   ├── cgroup/
│   │   └── cgroup.go        # cgroup v2 management
│   ├── namespace/
│   │   └── namespace.go     # Linux namespace operations
│   ├── rootfs/
│   │   └── rootfs.go        # Rootfs/image management
│   ├── network/
│   │   └── network.go       # Network namespace + veth
│   └── store/
│       └── store.go         # State persistence
├── go.mod
├── go.sum
└── README.md
```

## How It Works

### Container Creation Flow

1. **Image Check**: Verify image exists in `/var/lib/nanocontainer/images/`
2. **Rootfs Extract**: Unpack `.tar.gz` to `/var/lib/nanocontainer/containers/<id>/rootfs/`
3. **Base Setup**: Create `/proc`, `/sys`, `/dev`, `/tmp` directories
4. **Dev Nodes**: Create `/dev/null`, `/dev/zero`, `/dev/random`, `/dev/tty`
5. **Network**: Create veth pair, attach to bridge, configure IP
6. **Clone**: Fork process with `CLONE_NEWPID|CLONE_NEWNS|CLONE_NEWUTS|CLONE_NEWNET`
7. **Init (PID 1)**: Mount /proc, /sys, /dev; pivot_root; exec user command
8. **Cgroup**: Add PID to cgroup with CPU/memory limits

### Namespace Isolation

| Namespace | Isolation | Syscall Flag |
|-----------|-----------|--------------|
| PID | Process ID tree | `CLONE_NEWPID` |
| Mount | Filesystem tree | `CLONE_NEWNS` |
| UTS | Hostname/domainname | `CLONE_NEWUTS` |
| Network | Network stack | `CLONE_NEWNET` |

### Cgroup v2 Limits

| Resource | File | Format |
|----------|------|--------|
| CPU | `cpu.max` | `<quota> <period>` |
| Memory | `memory.max` | `<bytes>` |
| PIDs | `pids.max` | `<count>` |

## Limitations

- **Linux only**: Uses Linux-specific syscalls (clone, pivot_root, cgroups)
- **Root required**: Namespaces and cgroups require root privileges
- **No image registry**: Manual image management (tar.gz files)
- **No overlayfs**: Uses plain directory extraction
- **No seccomp**: No syscall filtering (can be added)

## Security Considerations

- Containers run with root privileges by default
- No AppArmor/SELinux profiles
- No seccomp filters
- No user namespace remapping (can be added)
- Network bridge is accessible to all containers

## License

MIT
