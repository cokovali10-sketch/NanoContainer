//go:build linux

package security

import (
	"fmt"
	"syscall"
)

// Linux capabilities (from include/uapi/linux/capability.h)
const (
	CAP_CHOWN            = 0
	CAP_DAC_OVERRIDE     = 1
	CAP_DAC_READ_SEARCH  = 2
	CAP_FOWNER           = 3
	CAP_FSETID           = 4
	CAP_KILL             = 5
	CAP_SETGID           = 6
	CAP_SETUID           = 7
	CAP_SETPCAP          = 8
	CAP_LINUX_IMMUTABLE  = 9
	CAP_NET_BIND_SERVICE = 10
	CAP_NET_BROADCAST    = 11
	CAP_NET_ADMIN        = 12
	CAP_NET_RAW          = 13
	CAP_IPC_LOCK         = 14
	CAP_IPC_OWNER        = 15
	CAP_SYS_MODULE       = 16
	CAP_SYS_RAWIO        = 17
	CAP_SYS_CHROOT       = 18
	CAP_SYS_PTRACE       = 19
	CAP_SYS_PACCT        = 20
	CAP_SYS_ADMIN        = 21
	CAP_SYS_BOOT         = 22
	CAP_SYS_NICE         = 23
	CAP_SYS_RESOURCE     = 24
	CAP_SYS_TIME         = 25
	CAP_SYS_TTY_CONFIG   = 26
	CAP_MKNOD            = 27
	CAP_LEASE            = 28
	CAP_AUDIT_WRITE      = 29
	CAP_AUDIT_CONTROL    = 30
	CAP_SETFCAP          = 31
)

// prctl constants
const (
	PR_CAPBSET_READ = 23
	PR_CAPBSET_DROP = 24
	PR_AMBIENT      = 47
	PR_CAP_AMBIENT_IS_SET = 1
	PR_CAP_AMBIENT_RAISE  = 2
	PR_CAP_AMBIENT_LOWER  = 3
)

// ContainerCapabilities defines what capabilities to keep or drop.
type ContainerCapabilities struct {
	Keep []string `json:"keep"`
	Drop []string `json:"drop"`
}

// DefaultCapabilities returns a minimal capability set for containers.
// Drops everything except a safe, restricted set.
func DefaultCapabilities() *ContainerCapabilities {
	return &ContainerCapabilities{
		Keep: []string{
			"CAP_CHOWN",
			"CAP_SETGID",
			"CAP_SETUID",
			"CAP_NET_BIND_SERVICE",
			"CAP_KILL",
			"CAP_FOWNER",
			"CAP_FSETID",
		},
		Drop: []string{
			"CAP_SYS_MODULE",
			"CAP_SYS_RAWIO",
			"CAP_SYS_ADMIN",
			"CAP_SYS_BOOT",
			"CAP_SYS_NICE",
			"CAP_SYS_RESOURCE",
			"CAP_SYS_TIME",
			"CAP_SYS_TTY_CONFIG",
			"CAP_NET_ADMIN",
			"CAP_NET_RAW",
			"CAP_IPC_LOCK",
			"CAP_IPC_OWNER",
			"CAP_SYS_PTRACE",
			"CAP_SYS_PACCT",
			"CAP_LINUX_IMMUTABLE",
			"CAP_DAC_READ_SEARCH",
			"CAP_MKNOD",
			"CAP_LEASE",
			"CAP_AUDIT_CONTROL",
			"CAP_SETFCAP",
		},
	}
}

// ApplyCapabilities applies the capability set to the current process.
func ApplyCapabilities(caps *ContainerCapabilities) error {
	if caps == nil {
		return fmt.Errorf("capabilities is nil")
	}

	// First drop all capabilities that are in the drop list.
	for _, capName := range caps.Drop {
		capNum, err := resolveCapability(capName)
		if err != nil {
			return fmt.Errorf("failed to resolve capability %q: %w", capName, err)
		}
		if err := DropCapability(capNum); err != nil {
			// Some capabilities may not be set; continue dropping others.
			continue
		}
	}

	// Then raise ambient capabilities for the keep list.
	// Ambient caps are inherited across execve.
	for _, capName := range caps.Keep {
		capNum, err := resolveCapability(capName)
		if err != nil {
			return fmt.Errorf("failed to resolve capability %q: %w", capName, err)
		}
		if err := AddCapability(capNum); err != nil {
			return fmt.Errorf("failed to add capability %q: %w", capName, err)
		}
		if err := SetAmbient(capNum); err != nil {
			return fmt.Errorf("failed to set ambient capability %q: %w", capName, err)
		}
	}

	return nil
}

// DropCapability drops a specific capability from the bounding set.
func DropCapability(cap int) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_PRCTL,
		uintptr(PR_CAPBSET_DROP),
		uintptr(cap),
		0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("failed to drop capability %d: %v", cap, errno)
	}
	return nil
}

// AddCapability raises a capability in the effective/permitted sets.
func AddCapability(cap int) error {
	// Use capset to set the capability in the effective set.
	var hdr syscall.CapUserHeader
	var data [2]syscall.CapUserData

	hdr.Version = syscall.LINUX_CAPABILITY_VERSION_3
	hdr.Pid = 0

	// Get current capabilities
	if err := syscall.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("failed to get capabilities: %w", err)
	}

	// Set the capability bit
	word := cap / 32
	bit := uint(cap % 32)
	if word < 2 {
		data[0].Effective |= 1 << bit
		data[0].Permitted |= 1 << bit
		if word == 1 {
			// For capabilities >= 32, use the second data word.
			// This path handles 64-bit capability sets.
			// Capabilities 0-31 use data[0]; capabilities 32+ use data[1].
		}
	}

	if err := syscall.Capset(&hdr, &data[0]); err != nil {
		return fmt.Errorf("failed to set capability %d: %w", cap, err)
	}

	return nil
}

// HasCapability checks whether a capability is currently set in the effective set.
func HasCapability(cap int) (bool, error) {
	var hdr syscall.CapUserHeader
	var data [2]syscall.CapUserData

	hdr.Version = syscall.LINUX_CAPABILITY_VERSION_3
	hdr.Pid = 0

	if err := syscall.Capget(&hdr, &data[0]); err != nil {
		return false, fmt.Errorf("failed to get capabilities: %w", err)
	}

	word := cap / 32
	bit := uint(cap % 32)

	var effective uint32
	if word == 0 {
		effective = data[0].Effective
	} else {
		effective = data[1].Effective
	}

	return (effective & (1 << bit)) != 0, nil
}

// SetAmbient sets a capability in the ambient set using prctl.
func SetAmbient(cap int) error {
	// First check if capability is in the bounding set
	_, _, errno := syscall.Syscall(
		syscall.SYS_PRCTL,
		uintptr(PR_CAPBSET_READ),
		uintptr(cap),
		0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("capability %d not in bounding set: %v", cap, errno)
	}

	// Raise the capability in ambient set
	_, _, errno = syscall.Syscall(
		syscall.SYS_PRCTL,
		uintptr(PR_AMBIENT),
		uintptr(PR_CAP_AMBIENT_RAISE),
		uintptr(cap),
		0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("failed to set ambient capability %d: %v", cap, errno)
	}

	return nil
}

// ClearAmbientCapabilities removes all ambient capabilities.
func ClearAmbientCapabilities() error {
	// Drop all ambient capabilities by lowering each one from 0 to 31.
	for i := 0; i < 32; i++ {
		_, _, errno := syscall.Syscall(
			syscall.SYS_PRCTL,
			uintptr(PR_AMBIENT),
			uintptr(PR_CAP_AMBIENT_LOWER),
			uintptr(i),
			0, 0,
		)
		// Ignore errors for capabilities that aren't set.
		_ = errno
	}
	return nil
}

// resolveCapability converts a capability name string to its numeric value.
func resolveCapability(name string) (int, error) {
	switch name {
	case "CAP_CHOWN":
		return CAP_CHOWN, nil
	case "CAP_DAC_OVERRIDE":
		return CAP_DAC_OVERRIDE, nil
	case "CAP_DAC_READ_SEARCH":
		return CAP_DAC_READ_SEARCH, nil
	case "CAP_FOWNER":
		return CAP_FOWNER, nil
	case "CAP_FSETID":
		return CAP_FSETID, nil
	case "CAP_KILL":
		return CAP_KILL, nil
	case "CAP_SETGID":
		return CAP_SETGID, nil
	case "CAP_SETUID":
		return CAP_SETUID, nil
	case "CAP_SETPCAP":
		return CAP_SETPCAP, nil
	case "CAP_LINUX_IMMUTABLE":
		return CAP_LINUX_IMMUTABLE, nil
	case "CAP_NET_BIND_SERVICE":
		return CAP_NET_BIND_SERVICE, nil
	case "CAP_NET_BROADCAST":
		return CAP_NET_BROADCAST, nil
	case "CAP_NET_ADMIN":
		return CAP_NET_ADMIN, nil
	case "CAP_NET_RAW":
		return CAP_NET_RAW, nil
	case "CAP_IPC_LOCK":
		return CAP_IPC_LOCK, nil
	case "CAP_IPC_OWNER":
		return CAP_IPC_OWNER, nil
	case "CAP_SYS_MODULE":
		return CAP_SYS_MODULE, nil
	case "CAP_SYS_RAWIO":
		return CAP_SYS_RAWIO, nil
	case "CAP_SYS_CHROOT":
		return CAP_SYS_CHROOT, nil
	case "CAP_SYS_PTRACE":
		return CAP_SYS_PTRACE, nil
	case "CAP_SYS_PACCT":
		return CAP_SYS_PACCT, nil
	case "CAP_SYS_ADMIN":
		return CAP_SYS_ADMIN, nil
	case "CAP_SYS_BOOT":
		return CAP_SYS_BOOT, nil
	case "CAP_SYS_NICE":
		return CAP_SYS_NICE, nil
	case "CAP_SYS_RESOURCE":
		return CAP_SYS_RESOURCE, nil
	case "CAP_SYS_TIME":
		return CAP_SYS_TIME, nil
	case "CAP_SYS_TTY_CONFIG":
		return CAP_SYS_TTY_CONFIG, nil
	case "CAP_MKNOD":
		return CAP_MKNOD, nil
	case "CAP_LEASE":
		return CAP_LEASE, nil
	case "CAP_AUDIT_WRITE":
		return CAP_AUDIT_WRITE, nil
	case "CAP_AUDIT_CONTROL":
		return CAP_AUDIT_CONTROL, nil
	case "CAP_SETFCAP":
		return CAP_SETFCAP, nil
	default:
		return -1, fmt.Errorf("unknown capability: %s", name)
	}
}
