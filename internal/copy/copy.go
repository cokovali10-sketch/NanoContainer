//go:build linux

package copy

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type CopyOptions struct {
	ContainerID string
	Source      string
	Destination string
	Direction   string
}

func loadRootfs(containerID string, cfg config.Config) (string, error) {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return "", fmt.Errorf("load container state: %w", err)
	}
	if state.RootfsPath == "" {
		return "", fmt.Errorf("container %s has no rootfs path", containerID)
	}
	if _, err := os.Stat(state.RootfsPath); err != nil {
		return "", fmt.Errorf("rootfs path %s does not exist: %w", state.RootfsPath, err)
	}
	return state.RootfsPath, nil
}

func CopyToContainer(opts CopyOptions, cfg config.Config) error {
	rootfs, err := loadRootfs(opts.ContainerID, cfg)
	if err != nil {
		return err
	}
	destPath := filepath.Join(rootfs, strings.TrimPrefix(opts.Destination, "/"))
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}
	if opts.Source == "-" {
		return extractTar(os.Stdin, filepath.Dir(destPath))
	}
	srcInfo, err := os.Stat(opts.Source)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if srcInfo.IsDir() {
		return copyDirToContainer(opts.Source, destPath)
	}
	return copyFileToContainer(opts.Source, destPath)
}

func CopyFromContainer(opts CopyOptions, cfg config.Config) error {
	rootfs, err := loadRootfs(opts.ContainerID, cfg)
	if err != nil {
		return err
	}
	srcPath := filepath.Join(rootfs, strings.TrimPrefix(opts.Source, "/"))
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("source path %s not found in container: %w", opts.Source, err)
	}
	destPath := opts.Destination
	if destPath == "-" {
		tw := tar.NewWriter(os.Stdout)
		defer tw.Close()
		return addTarEntry(tw, srcPath, rootfs)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if srcInfo.IsDir() {
		return copyDirFromContainer(srcPath, destPath)
	}
	return copyFileFromContainer(srcPath, destPath)
}

func copyDirToContainer(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relPath)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFileToContainer(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	return copyFile(src, dest, info.Mode())
}

func copyDirFromContainer(src, dest string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, relPath)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFileFromContainer(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	return copyFile(src, dest, info.Mode())
}

func copyFile(src, dest string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()
	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer destFile.Close()
	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	return destFile.Close()
}

func createTar(src string) (io.Reader, error) {
	pr, pw := io.Pipe()
	tw := tar.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer tw.Close()
		src = filepath.Clean(src)
		err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(filepath.Dir(src), path)
			if err != nil {
				return err
			}
			if relPath == "." {
				return nil
			}
			return addTarEntry(tw, path, filepath.Dir(src))
		})
		if err != nil {
			pw.CloseWithError(err)
		}
	}()
	return pr, nil
}

func addTarEntry(tw *tar.Writer, path, baseDir string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		return err
	}
	relPath = filepath.ToSlash(relPath)
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("file info header: %w", err)
	}
	header.Name = relPath
	if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
		header.Name += "/"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		linkTarget, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("readlink %s: %w", path, err)
		}
		header.Linkname = linkTarget
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write header %s: %w", relPath, err)
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy %s: %w", path, err)
	}
	return nil
}

func extractTar(tr io.Reader, dest string) error {
	tarReader := tar.NewReader(tr)
	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		target := filepath.Join(dest, hdr.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			f.Close()
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
