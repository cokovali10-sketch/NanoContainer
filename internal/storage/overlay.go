//go:build linux

package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type OverlayFS struct {
	BaseDir string
}

type OverlayConfig struct {
	LowerDir string
	UpperDir string
	WorkDir  string
	MergeDir string
}

func NewOverlayFS(baseDir string) *OverlayFS {
	return &OverlayFS{BaseDir: baseDir}
}

func (o *OverlayFS) SetupOverlay(containerID string, lowerDirs []string) (string, error) {
	config := o.getOverlayConfig(containerID)

	dirs := []string{config.UpperDir, config.WorkDir, config.MergeDir}
	for _, d := range dirs {
		if err := mkdirAll(d); err != nil {
			return "", fmt.Errorf("overlay: failed to create dir %s: %w", d, err)
		}
	}

	for _, ld := range lowerDirs {
		if _, err := os.Stat(ld); os.IsNotExist(err) {
			return "", fmt.Errorf("overlay: lower dir does not exist: %s", ld)
		}
	}

	lowerDirStr := strings.Join(lowerDirs, ":")
	if err := mountOverlay(lowerDirStr, config.UpperDir, config.WorkDir, config.MergeDir); err != nil {
		return "", fmt.Errorf("overlay: mount failed: %w", err)
	}

	return config.MergeDir, nil
}

func (o *OverlayFS) GetUpperDir(containerID string) string {
	return filepath.Join(o.BaseDir, containerID, "upper")
}

func (o *OverlayFS) GetMergeDir(containerID string) string {
	return filepath.Join(o.BaseDir, containerID, "merged")
}

func (o *OverlayFS) Commit(containerID string) (string, error) {
	config := o.getOverlayConfig(containerID)

	if _, err := os.Stat(config.UpperDir); os.IsNotExist(err) {
		return "", fmt.Errorf("overlay: upper dir does not exist for container %s", containerID)
	}

	newLower := filepath.Join(o.BaseDir, containerID, fmt.Sprintf("lower-%d", timeNowUnix()))
	if err := mkdirAll(newLower); err != nil {
		return "", fmt.Errorf("overlay: failed to create new lower: %w", err)
	}

	if err := copyDir(config.UpperDir, newLower); err != nil {
		return "", fmt.Errorf("overlay: failed to copy upper to new lower: %w", err)
	}

	upperFiles, _ := os.ReadDir(config.UpperDir)
	for _, f := range upperFiles {
		src := filepath.Join(config.UpperDir, f.Name())
		dst := filepath.Join(newLower, f.Name())
		if f.IsDir() {
			copyDir(src, dst)
		} else {
			copyFile(src, dst)
		}
	}

	if err := cleanDir(config.UpperDir); err != nil {
		return "", fmt.Errorf("overlay: failed to clean upper: %w", err)
	}

	return newLower, nil
}

func (o *OverlayFS) Cleanup(containerID string) error {
	config := o.getOverlayConfig(containerID)

	umountOverlay(config.MergeDir)

	dirs := []string{config.MergeDir, config.WorkDir, config.UpperDir}
	for _, d := range dirs {
		os.RemoveAll(d)
	}

	return nil
}

func (o *OverlayFS) ListLayers() ([]string, error) {
	var layers []string

	entries, err := os.ReadDir(o.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("overlay: failed to read base dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		containerDir := filepath.Join(o.BaseDir, entry.Name())
		lowerDirs, err := filepath.Glob(filepath.Join(containerDir, "lower-*"))
		if err == nil {
			layers = append(layers, lowerDirs...)
		}
	}

	return layers, nil
}

func (o *OverlayFS) GetDiskUsage(containerID string) (int64, error) {
	config := o.getOverlayConfig(containerID)

	var totalSize int64
	err := filepath.Walk(config.UpperDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("overlay: failed to calculate disk usage: %w", err)
	}

	return totalSize, nil
}

func (o *OverlayFS) Diff(containerID, layer1, layer2 string) ([]string, error) {
	files1 := make(map[string]os.FileInfo)
	files2 := make(map[string]os.FileInfo)

	err := filepath.Walk(layer1, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(layer1, path)
		files1[relPath] = info
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("overlay: failed to walk layer1: %w", err)
	}

	err = filepath.Walk(layer2, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, _ := filepath.Rel(layer2, path)
		files2[relPath] = info
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("overlay: failed to walk layer2: %w", err)
	}

	var diffs []string
	for path, info1 := range files1 {
		info2, exists := files2[path]
		if !exists {
			diffs = append(diffs, fmt.Sprintf("+%s", path))
		} else if info1.Size() != info2.Size() || info1.ModTime().After(info2.ModTime()) {
			diffs = append(diffs, fmt.Sprintf("~%s", path))
		}
	}

	for path := range files2 {
		if _, exists := files1[path]; !exists {
			diffs = append(diffs, fmt.Sprintf("-%s", path))
		}
	}

	return diffs, nil
}

func (o *OverlayFS) getOverlayConfig(containerID string) *OverlayConfig {
	return &OverlayConfig{
		UpperDir: filepath.Join(o.BaseDir, containerID, "upper"),
		WorkDir:  filepath.Join(o.BaseDir, containerID, "work"),
		MergeDir: filepath.Join(o.BaseDir, containerID, "merged"),
	}
}

func mkdirAll(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}
	return nil
}

func mountOverlay(lower, upper, work, merged string) error {
	data := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lower, upper, work)
	err := syscall.Mount("overlay", merged, "overlay", 0, data)
	if err != nil {
		return fmt.Errorf("mount failed: %w", err)
	}
	return nil
}

func umountOverlay(path string) error {
	err := syscall.Unmount(path, 0)
	if err != nil && err != syscall.EINVAL {
		return fmt.Errorf("umount failed: %w", err)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		return copyFile(path, dstPath)
	})
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func timeNowUnix() int64 {
	return int64(os.Getpid())
}
