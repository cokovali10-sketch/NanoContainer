//go:build linux

package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	StdoutLog = "stdout.log"
	StderrLog = "stderr.log"
)

type Config struct {
	Hostname   string
	RootfsPath string
	EnablePID  bool
	EnableMount bool
	EnableUTS  bool
	EnableNet  bool
	EnableUser bool
}

func CloneFlags(cfg Config) uintptr {
	var flags uintptr
	if cfg.EnablePID {
		flags |= syscall.CLONE_NEWPID
	}
	if cfg.EnableMount {
		flags |= syscall.CLONE_NEWNS
	}
	if cfg.EnableUTS {
		flags |= syscall.CLONE_NEWUTS
	}
	if cfg.EnableNet {
		flags |= syscall.CLONE_NEWNET
	}
	if cfg.EnableUser {
		flags |= syscall.CLONE_NEWUSER
	}
	return flags
}

func SetupHostname(name string) error {
	if name == "" {
		return nil
	}
	if err := syscall.Sethostname([]byte(name)); err != nil {
		return fmt.Errorf("set hostname: %w", err)
	}
	if err := os.WriteFile("/proc/sys/kernel/hostname", []byte(name+"\n"), 0644); err != nil {
		return fmt.Errorf("write hostname: %w", err)
	}
	return nil
}

func PivotRoot(newRoot string) error {
	if err := os.Chdir(newRoot); err != nil {
		return fmt.Errorf("chdir to new root: %w", err)
	}

	putOld := filepath.Join(newRoot, ".pivot_old")
	if err := os.MkdirAll(putOld, 0700); err != nil {
		return fmt.Errorf("create pivot_old dir: %w", err)
	}

	if err := syscall.PivotRoot(newRoot, putOld); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	if err := os.Chdir("/"); err != nil {
		return fmt.Errorf("chdir to /: %w", err)
	}

	putOldPath := "/.pivot_old"
	if err := syscall.Unmount(putOldPath, syscall.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old root: %w", err)
	}
	if err := os.RemoveAll(putOldPath); err != nil {
		return fmt.Errorf("remove old root: %w", err)
	}
	return nil
}

func MountProc(rootfs string) error {
	procPath := filepath.Join(rootfs, "proc")
	if err := os.MkdirAll(procPath, 0555); err != nil {
		return fmt.Errorf("create /proc dir: %w", err)
	}
	return syscall.Mount("proc", procPath, "proc", 0, "")
}

func MountSys(rootfs string) error {
	sysPath := filepath.Join(rootfs, "sys")
	if err := os.MkdirAll(sysPath, 0555); err != nil {
		return fmt.Errorf("create /sys dir: %w", err)
	}
	return syscall.Mount("sysfs", sysPath, "sysfs", syscall.MS_RDONLY|syscall.MS_NOSUID|syscall.MS_NODEV, "")
}

func MountDevtmpfs(rootfs string) error {
	devPath := filepath.Join(rootfs, "dev")
	if err := os.MkdirAll(devPath, 0755); err != nil {
		return fmt.Errorf("create /dev dir: %w", err)
	}
	return syscall.Mount("devtmpfs", devPath, "devtmpfs", syscall.MS_NOSUID|syscall.MS_STRICTATIME, "mode=0755")
}

func MountTmpfs(target string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("create tmpfs dir: %w", err)
	}
	return syscall.Mount("tmpfs", target, "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_STRICTATIME, "size=64m")
}

func BindMount(source, target string) error {
	if err := os.MkdirAll(target, 0755); err != nil {
		return fmt.Errorf("create bind target: %w", err)
	}
	return syscall.Mount(source, target, "", syscall.MS_BIND|syscall.MS_REC, "")
}

func CreateBaseDirs(rootfs string) error {
	dirs := []string{
		"/bin", "/sbin", "/usr/bin", "/usr/sbin",
		"/lib", "/lib64", "/usr/lib",
		"/etc", "/tmp", "/var", "/root",
		"/proc", "/sys", "/dev", "/run",
		"/mnt", "/opt", "/srv", "/home",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(rootfs, d), 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

func CreateDevNodes(rootfs string) error {
	devices := []struct {
		path string
		mode uint32
		maj  uint32
		min  uint32
	}{
		{"/dev/null", 0666, 1, 3},
		{"/dev/zero", 0666, 1, 5},
		{"/dev/random", 0666, 1, 8},
		{"/dev/urandom", 0666, 1, 9},
		{"/dev/tty", 0666, 5, 0},
		{"/dev/console", 0600, 5, 1},
		{"/dev/ptmx", 0666, 5, 2},
	}
	for _, d := range devices {
		full := filepath.Join(rootfs, d.path)
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			continue
		}
		if err := syscall.Mknod(full, d.mode|syscall.S_IFCHR, int(mknodDev(d.maj, d.min))); err != nil {
			if os.IsExist(err) {
				continue
			}
			return fmt.Errorf("mknod %s: %w", d.path, err)
		}
	}
	ptsDir := filepath.Join(rootfs, "/dev/pts")
	if err := os.MkdirAll(ptsDir, 0755); err != nil {
		return err
	}
	if err := syscall.Mount("devpts", ptsDir, "devpts", 0, "gid=5,mode=620"); err != nil {
		return fmt.Errorf("mount devpts: %w", err)
	}
	return nil
}

func mknodDev(major, minor uint32) uint64 {
	return (uint64(major) << 8) | uint64(minor)
}

func MknodDev(path string, mode uint32, maj, min uint32) error {
	return syscall.Mknod(path, mode, int(mknodDev(maj, min)))
}

func UmountRecursive(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		sub := filepath.Join(path, e.Name())
		if e.IsDir() {
			_ = syscall.Unmount(sub, syscall.MNT_DETACH)
		}
	}
	return syscall.Unmount(path, syscall.MNT_DETACH)
}

func Chroot(path string) error {
	if err := syscall.Chroot(path); err != nil {
		return fmt.Errorf("chroot: %w", err)
	}
	return syscall.Chdir("/")
}
