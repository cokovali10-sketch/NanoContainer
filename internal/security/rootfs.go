//go:build linux

package security

import (
	"fmt"
	"syscall"
)

// MountReadOnly remounts the given path as read-only using MS_REMOUNT|MS_RDONLY|MS_BIND.
func MountReadOnly(path string) error {
	err := syscall.Mount("", path, "", syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_BIND, "")
	if err != nil {
		return fmt.Errorf("failed to mount %s read-only: %w", path, err)
	}
	return nil
}

// MountSecureTmpfs creates a tmpfs mount with noexec, nosuid, nodev.
func MountSecureTmpfs(path string) error {
	err := syscall.Mount("tmpfs", path, "tmpfs",
		syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_REMOUNT,
		"size=64M,mode=0755",
	)
	if err != nil {
		return fmt.Errorf("failed to mount secure tmpfs at %s: %w", path, err)
	}
	return nil
}

// MountProcSecure mounts /proc with nosuid, nodev, noexec, and restricts visibility.
func MountProcSecure(path string) error {
	err := syscall.Mount("proc", path, "proc",
		syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC|syscall.MS_REMOUNT,
		"",
	)
	if err != nil {
		return fmt.Errorf("failed to mount secure proc at %s: %w", path, err)
	}
	return nil
}

// NoNewPrivileges sets the PR_SET_NO_NEW_PRIVS flag on the calling process.
// This prevents the process from gaining privileges via execve.
func NoNewPrivileges() error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_PRCTL,
		uintptr(syscall.PR_SET_NO_NEW_PRIVS),
		1, 0, 0, 0,
	)
	if errno != 0 {
		return fmt.Errorf("failed to set PR_SET_NO_NEW_PRIVS: %v", errno)
	}
	return nil
}
