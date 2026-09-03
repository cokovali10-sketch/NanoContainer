//go:build linux

package rootfs

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Manager struct {
	ImageDir    string
	ContainerDir string
}

func New(imageDir, containerDir string) *Manager {
	return &Manager{
		ImageDir:    imageDir,
		ContainerDir: containerDir,
	}
}

func (m *Manager) ListImages() ([]string, error) {
	entries, err := os.ReadDir(m.ImageDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var images []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar.gz") {
			images = append(images, strings.TrimSuffix(e.Name(), ".tar.gz"))
		}
	}
	return images, nil
}

func (m *Manager) ImageExists(name string) bool {
	path := filepath.Join(m.ImageDir, name+".tar.gz")
	_, err := os.Stat(path)
	return err == nil
}

func (m *Manager) PullImage(name, url string) error {
	if err := os.MkdirAll(m.ImageDir, 0755); err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}
	path := filepath.Join(m.ImageDir, name+".tar.gz")
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("write image: %w", err)
	}
	out.Close()
	return os.Rename(tmp, path)
}

func (m *Manager) UnpackImage(name, dest string) error {
	tarPath := filepath.Join(m.ImageDir, name+".tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		return fmt.Errorf("image %s not found", name)
	}
	return extractTarGz(tarPath, dest)
}

func extractTarGz(tarPath, dest string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open tarball: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		target := filepath.Join(dest, hdr.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("write file %s: %w", target, err)
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return fmt.Errorf("symlink %s: %w", target, err)
			}
		case tar.TypeLink:
			linkTarget := filepath.Join(dest, hdr.Linkname)
			if err := os.Link(linkTarget, target); err != nil {
				return fmt.Errorf("hardlink %s: %w", target, err)
			}
		}
	}
	return nil
}

func (m *Manager) PrepareRootfs(imageName, containerID string) (string, error) {
	rootfs := filepath.Join(m.ContainerDir, containerID, "rootfs")
	if err := os.MkdirAll(rootfs, 0755); err != nil {
		return "", fmt.Errorf("create rootfs dir: %w", err)
	}

	if err := m.UnpackImage(imageName, rootfs); err != nil {
		os.RemoveAll(rootfs)
		return "", fmt.Errorf("unpack image: %w", err)
	}

	if err := m.SetupBaseDirs(rootfs); err != nil {
		os.RemoveAll(rootfs)
		return "", fmt.Errorf("setup base dirs: %w", err)
	}

	if err := m.SetupDevNodes(rootfs); err != nil {
		os.RemoveAll(rootfs)
		return "", fmt.Errorf("setup dev nodes: %w", err)
	}

	if err := m.SetupResolvConf(rootfs); err != nil {
		os.RemoveAll(rootfs)
		return "", fmt.Errorf("setup resolv.conf: %w", err)
	}

	return rootfs, nil
}

func (m *Manager) SetupBaseDirs(rootfs string) error {
	dirs := []string{
		"bin", "sbin", "usr/bin", "usr/sbin",
		"lib", "lib64", "usr/lib",
		"etc", "tmp", "var", "root",
		"proc", "sys", "dev", "run",
		"mnt", "opt", "srv", "home",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(rootfs, d), 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	return nil
}

func (m *Manager) SetupDevNodes(rootfs string) error {
	type device struct {
		path string
		mode uint32
		maj  uint32
		min  uint32
	}
	devices := []device{
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
		os.Remove(full)
		devNum := (uint64(d.maj) << 8) | uint64(d.min)
		if err := syscall.Mknod(full, d.mode|syscall.S_IFCHR, int(devNum)); err != nil {
			if !os.IsExist(err) {
				continue
			}
		}
	}
	ptsDir := filepath.Join(rootfs, "dev/pts")
	os.MkdirAll(ptsDir, 0755)
	syscall.Mount("devpts", ptsDir, "devpts", 0, "gid=5,mode=620")
	return nil
}

func (m *Manager) SetupResolvConf(rootfs string) error {
	content := "nameserver 8.8.8.8\nnameserver 8.8.4.4\n"
	return os.WriteFile(filepath.Join(rootfs, "etc/resolv.conf"), []byte(content), 0644)
}

func (m *Manager) SetupHosts(rootfs string, hostname string) error {
	content := fmt.Sprintf("127.0.0.1\tlocalhost\n127.0.1.1\t%s\n::1\t\tlocalhost ip6-localhost ip6-loopback\n", hostname)
	return os.WriteFile(filepath.Join(rootfs, "etc/hosts"), []byte(content), 0644)
}

func (m *Manager) SetupHostnameFile(rootfs string, hostname string) error {
	return os.WriteFile(filepath.Join(rootfs, "etc/hostname"), []byte(hostname+"\n"), 0644)
}

func (m *Manager) HasBusybox(rootfs string) bool {
	_, err := os.Stat(filepath.Join(rootfs, "bin/sh"))
	return err == nil
}

func (m *Manager) InstallBusybox(rootfs string) error {
	cmd := exec.Command("cp", "-a", "/bin/busybox", filepath.Join(rootfs, "bin/busybox"))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy busybox: %w", err)
	}
	commands := []string{"sh", "ash", "ls", "cat", "echo", "mkdir", "rm", "cp", "mv", "ps", "grep", "sed", "awk", "vi", "wget", "tar", "gzip"}
	for _, c := range commands {
		target := filepath.Join(rootfs, "bin", c)
		os.Remove(target)
		os.Symlink("busybox", target)
	}
	return nil
}
