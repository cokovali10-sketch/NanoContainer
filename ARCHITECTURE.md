# Architecture

## System Overview

NanoContainer is a container runtime that implements Linux container primitives directly using syscalls. It provides a Docker-compatible interface while being completely independent of Docker.

## Core Components

### 1. Container Lifecycle

```
┌─────────────────────────────────────────────────────────────┐
│                    Container States                         │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Created ──→ Running ──→ Stopped                           │
│    │            │            │                              │
│    │            │            └──→ Removed                   │
│    │            │                                          │
│    │            └──→ Paused (future)                       │
│    │                                                       │
│    └──→ Failed (during creation)                           │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 2. Namespace Isolation

```
┌─────────────────────────────────────────────────────────────┐
│                    Host System                              │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                    Container                        │   │
│  │  ┌─────────────────────────────────────────────┐   │   │
│  │  │ PID Namespace                               │   │   │
│  │  │  PID 1: init process                        │   │   │
│  │  │  PID 2: user command                        │   │   │
│  │  └─────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────┐   │   │
│  │  │ Mount Namespace                             │   │   │
│  │  │  / (pivot_root)                             │   │   │
│  │  │  /proc (mounted)                            │   │   │
│  │  │  /sys (read-only)                           │   │   │
│  │  │  /dev (devtmpfs)                            │   │   │
│  │  └─────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────┐   │   │
│  │  │ UTS Namespace                               │   │   │
│  │  │  hostname: container-name                   │   │   │
│  │  └─────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────┐   │   │
│  │  │ Network Namespace                           │   │   │
│  │  │  eth0: veth-c-<id> (10.200.0.x/24)         │   │   │
│  │  │  lo: loopback                               │   │   │
│  │  │  route: default via 10.200.0.1              │   │   │
│  │  └─────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Bridge: nanocontainer0 (10.200.0.1/24)              │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 3. Storage Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    OverlayFS Mount                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Merged View (what container sees)                         │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  /bin   /lib   /etc   /app   /var                   │   │
│  └─────────────────────────────────────────────────────┘   │
│         ▲                ▲                ▲                │
│         │                │                │                │
│  ┌──────┴──────┐ ┌──────┴──────┐ ┌──────┴──────┐        │
│  │  Lower Dir  │ │  Lower Dir  │ │  Upper Dir  │        │
│  │  (alpine)   │ │  (base)     │ │  (writable) │        │
│  │  Read-Only  │ │  Read-Only  │ │  Read-Write │        │
│  └─────────────┘ └─────────────┘ └─────────────┘        │
│         │                │                │                │
│         └────────────────┴────────────────┘                │
│                    Work Dir                                │
│                    (temporary)                             │
└─────────────────────────────────────────────────────────────┘
```

### 4. Network Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Network Stack                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Container A          Container B          Host             │
│  ┌──────────┐         ┌──────────┐         │               │
│  │ eth0     │         │ eth0     │         │               │
│  │10.200.0.2│         │10.200.0.3│         │               │
│  └────┬─────┘         └────┬─────┘         │               │
│       │                    │                │               │
│  veth-c-a1b2          veth-c-c3d4           │               │
│       │                    │                │               │
│       └──────────┬─────────┘                │               │
│                  │                          │               │
│           ┌──────┴──────┐                   │               │
│           │  Bridge     │                   │               │
│           │ nanocont0   │                   │               │
│           │10.200.0.1   │                   │               │
│           └──────┬──────┘                   │               │
│                  │                          │               │
│           veth-h-bridge                     │               │
│                  │                          │               │
│           ┌──────┴──────┐                   │               │
│           │   iptables  │ ← NAT/MASQUERADE │               │
│           └──────┬──────┘                   │               │
│                  │                          │               │
│           ┌──────┴──────┐                   │               │
│           │  Internet   │                   │               │
│           └─────────────┘                   │               │
└─────────────────────────────────────────────────────────────┘
```

### 5. Security Layers

```
┌─────────────────────────────────────────────────────────────┐
│                    Security Stack                           │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Layer 7: No New Privileges (PR_SET_NO_NEW_PRIVS)         │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Prevents setuid/setgid escalation                  │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 6: Read-Only Rootfs                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Mount rootfs as MS_RDONLY                          │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 5: Linux Capabilities                               │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Drop: SYS_ADMIN, NET_RAW, SYS_MODULE, ...         │   │
│  │ Keep: NET_BIND_SERVICE, CHOWN, SETUID, ...         │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 4: Seccomp BPF                                      │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ Block 50+ dangerous syscalls                       │   │
│  │ mount, umount2, pivot_root, reboot, ptrace, ...    │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 3: Namespaces                                       │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ PID: Hide host processes                           │   │
│  │ Mount: Isolate filesystem                           │   │
│  │ Network: Isolate network stack                      │   │
│  │ UTS: Isolate hostname                               │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 2: cgroups                                           │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ CPU: Limit to X% per period                        │   │
│  │ Memory: Hard limit (OOM kill if exceeded)          │   │
│  │ PIDs: Prevent fork bombs                           │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  Layer 1: Encrypted Secrets                                │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ AES-256-GCM encryption at rest                     │   │
│  │ Secure key derivation (SHA-256)                    │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### 6. API Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Request Flow                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Client (curl/browser)                                     │
│       │                                                    │
│       ▼                                                    │
│  ┌─────────────┐                                           │
│  │   HTTP      │                                           │
│  │  Server     │                                           │
│  └──────┬──────┘                                           │
│         │                                                  │
│  ┌──────▼──────┐                                           │
│  │ Middleware   │                                           │
│  │ - CORS      │                                           │
│  │ - Logging   │                                           │
│  │ - Auth      │                                           │
│  │ - RateLimit │                                           │
│  └──────┬──────┘                                           │
│         │                                                  │
│  ┌──────▼──────┐                                           │
│  │   Router    │                                           │
│  └──────┬──────┘                                           │
│         │                                                  │
│  ┌──────▼──────────────────────────────────────────────┐   │
│  │                    Handlers                         │   │
│  ├────────────┬────────────┬────────────┬──────────────┤   │
│  │ Containers │   Images   │  Compose   │    Stats     │   │
│  │ CRUD       │   Pull/    │  Deploy/   │    System/   │   │
│  │ Exec       │   Build    │  Remove    │    Container │   │
│  └────────────┴────────────┴────────────┴──────────────┘   │
│         │                                                  │
│  ┌──────▼──────┐                                           │
│  │   Store     │                                           │
│  │ (JSON file) │                                           │
│  └─────────────┘                                           │
└─────────────────────────────────────────────────────────────┘
```

## File System Layout

```
/var/lib/nanocontainer/
├── images/
│   ├── alpine.tar.gz
│   ├── ubuntu.tar.gz
│   └── debian.tar.gz
├── containers/
│   ├── a1b2c3d4e5f6/
│   │   ├── state.json          # Container metadata
│   │   ├── rootfs/             # Container filesystem
│   │   │   ├── bin/
│   │   │   ├── etc/
│   │   │   ├── proc/
│   │   │   ├── sys/
│   │   │   └── dev/
│   │   ├── stdout.log          # Container stdout
│   │   └── stderr.log          # Container stderr
│   └── ...
├── volumes/
│   ├── app-data/
│   └── db-data/
├── secrets/
│   ├── db-password.enc
│   └── api-key.enc
├── network/
│   ├── a1b2c3d4.json          # Network config per container
│   └── ...
├── compose/
│   ├── webapp.json             # Stack state
│   └── ...
└── registry/
    ├── alpine/
    │   ├── latest/
    │   │   ├── manifest.json
    │   │   └── layer.tar.gz
    │   └── ...
    └── ...
```

## Syscall Reference

| Syscall | Purpose | Used In |
|---------|---------|---------|
| `clone` | Create process in new namespaces | container/init.go |
| `pivot_root` | Change root filesystem | namespace/namespace.go |
| `mount` | Mount filesystems | namespace/namespace.go |
| `umount2` | Unmount filesystems | namespace/namespace.go |
| `sethostname` | Set hostname | namespace/namespace.go |
| `chroot` | Change root directory | namespace/namespace.go |
| `prctl` | Process control (seccomp, caps) | security/*.go |
| `capset` | Set capabilities | security/capabilities.go |
| `seccomp` | Set seccomp filter | security/seccomp.go |
| `statfs` | Get filesystem stats | api/stats.go |
| `wait4` | Wait for process | container/container.go |
| `kill` | Send signal to process | container/container.go |
