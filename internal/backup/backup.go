//go:build linux

package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupManager handles container backups.
type BackupManager struct {
	BackupDir string
}

type BackupInfo struct {
	ID          string    `json:"id"`
	ContainerID string    `json:"container_id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
	Description string    `json:"description"`
}

type BackupOptions struct {
	ContainerID string
	Name        string
	Description string
	IncludeLogs bool
	IncludeData bool
}

// NewBackupManager creates a backup manager rooted at backupDir.
func NewBackupManager(backupDir string) *BackupManager {
	return &BackupManager{BackupDir: backupDir}
}

// CreateBackup creates a full container backup as a tar.gz archive.
func (bm *BackupManager) CreateBackup(opts BackupOptions) (*BackupInfo, error) {
	if opts.ContainerID == "" {
		return nil, fmt.Errorf("container ID is required")
	}

	backupID := generateID(opts.ContainerID)
	backupDir := filepath.Join(bm.BackupDir, backupID)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup dir: %w", err)
	}

	archivePath := filepath.Join(backupDir, "backup.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	rootfsDir := filepath.Join("/var/lib/nanocontainer/containers", opts.ContainerID, "rootfs")
	if err := addDirToTar(tw, rootfsDir, "rootfs"); err != nil {
		return nil, fmt.Errorf("archive rootfs: %w", err)
	}

	if opts.IncludeLogs {
		logDir := filepath.Join("/var/lib/nanocontainer/containers", opts.ContainerID, "logs")
		if err := addDirToTar(tw, logDir, "logs"); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("archive logs: %w", err)
		}
	}

	if opts.IncludeData {
		dataDir := filepath.Join("/var/lib/nanocontainer/containers", opts.ContainerID, "data")
		if err := addDirToTar(tw, dataDir, "data"); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("archive data: %w", err)
		}
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close archive file: %w", err)
	}

	stat, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}

	info := &BackupInfo{
		ID:          backupID,
		ContainerID: opts.ContainerID,
		Name:        opts.Name,
		Size:        stat.Size(),
		CreatedAt:   time.Now(),
		Description: opts.Description,
	}

	metaPath := filepath.Join(backupDir, "metadata.json")
	metaData, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}

	return info, nil
}

// RestoreBackup restores a container from a backup archive.
func (bm *BackupManager) RestoreBackup(backupID string, newContainerID string) (string, error) {
	backupDir := filepath.Join(bm.BackupDir, backupID)
	archivePath := filepath.Join(backupDir, "backup.tar.gz")

	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		return "", fmt.Errorf("backup %s not found", backupID)
	}

	destDir := filepath.Join("/var/lib/nanocontainer/containers", newContainerID, "rootfs")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create restore dir: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}

		target := filepath.Join(destDir, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", fmt.Errorf("create dir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", fmt.Errorf("create parent dir: %w", err)
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return "", fmt.Errorf("create file %s: %w", target, err)
			}
			if _, err := io.Copy(outFile, tr); err != nil {
				outFile.Close()
				return "", fmt.Errorf("write file %s: %w", target, err)
			}
			outFile.Close()
		}
	}

	return destDir, nil
}

// ListBackups lists all backups stored in the backup directory.
func (bm *BackupManager) ListBackups() ([]*BackupInfo, error) {
	entries, err := os.ReadDir(bm.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var backups []*BackupInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(bm.BackupDir, entry.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var info BackupInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		backups = append(backups, &info)
	}
	return backups, nil
}

// DeleteBackup removes a backup by ID.
func (bm *BackupManager) DeleteBackup(backupID string) error {
	backupDir := filepath.Join(bm.BackupDir, backupID)
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		return fmt.Errorf("backup %s not found", backupID)
	}
	return os.RemoveAll(backupDir)
}

// ExportBackup exports a backup to an external file path.
func (bm *BackupManager) ExportBackup(backupID, destPath string) error {
	srcPath := filepath.Join(bm.BackupDir, backupID, "backup.tar.gz")
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("backup %s not found", backupID)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// ImportBackup imports a backup from an external file path.
func (bm *BackupManager) ImportBackup(srcPath string) (string, error) {
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return "", fmt.Errorf("source file %s not found", srcPath)
	}

	backupID := generateID("imported")
	backupDir := filepath.Join(bm.BackupDir, backupID)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(filepath.Join(backupDir, "backup.tar.gz"))
	if err != nil {
		return "", fmt.Errorf("create dest: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy: %w", err)
	}

	info := &BackupInfo{
		ID:        backupID,
		Name:      filepath.Base(srcPath),
		CreatedAt: time.Now(),
	}
	metaData, _ := json.Marshal(info)
	_ = os.WriteFile(filepath.Join(backupDir, "metadata.json"), metaData, 0o644)

	return backupID, nil
}

// GetBackupSize returns the total size in bytes of a backup.
func (bm *BackupManager) GetBackupSize(backupID string) (int64, error) {
	backupDir := filepath.Join(bm.BackupDir, backupID)
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		return 0, fmt.Errorf("backup %s not found", backupID)
	}

	var totalSize int64
	err := filepath.Walk(backupDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	return totalSize, err
}

// CleanupOldBackups removes backups older than keepDays days.
func (bm *BackupManager) CleanupOldBackups(keepDays int) (int, error) {
	backups, err := bm.ListBackups()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().AddDate(0, 0, -keepDays)
	removed := 0

	for _, b := range backups {
		if b.CreatedAt.Before(cutoff) {
			if err := bm.DeleteBackup(b.ID); err != nil {
				return removed, fmt.Errorf("delete backup %s: %w", b.ID, err)
			}
			removed++
		}
	}
	return removed, nil
}

func addDirToTar(tw *tar.Writer, dir, prefix string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.Join(prefix, rel)
		name = filepath.ToSlash(name)

		if info.IsDir() {
			name += "/"
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
}

func generateID(tag string) string {
	ts := time.Now().Format("20060102-150405")
	suffix := strings.ReplaceAll(ts, "-", "")
	return fmt.Sprintf("%s-%s-%s", tag, suffix, randomHex(4))
}

func randomHex(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	f, _ := os.Open("/dev/urandom")
	if f != nil {
		io.ReadFull(f, b)
		f.Close()
	}
	for i := range b {
		b[i] = hex[b[i]%16]
	}
	return string(b)
}
