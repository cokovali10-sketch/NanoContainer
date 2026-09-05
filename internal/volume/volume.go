//go:build linux

package volume

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type VolumeInfo struct {
	Name       string    `json:"name"`
	Driver     string    `json:"driver"`
	Mountpoint string    `json:"mountpoint"`
	CreatedAt  time.Time `json:"created_at"`
	Size       int64     `json:"size_bytes"`
}

type VolumeManager struct {
	volumesDir string
}

func NewVolumeManager(volumesDir string) *VolumeManager {
	os.MkdirAll(volumesDir, 0755)
	return &VolumeManager{volumesDir: volumesDir}
}

func (vm *VolumeManager) volumePath(name string) string {
	return filepath.Join(vm.volumesDir, name)
}

func (vm *VolumeManager) metadataPath(name string) string {
	return filepath.Join(vm.volumesDir, name, "meta.json")
}

func (vm *VolumeManager) dataPath(name string) string {
	return filepath.Join(vm.volumesDir, name, "data")
}

func (vm *VolumeManager) Create(name, driver string) error {
	if name == "" {
		return fmt.Errorf("volume name is required")
	}
	if driver == "" {
		driver = "local"
	}

	volPath := vm.volumePath(name)
	if _, err := os.Stat(volPath); err == nil {
		return fmt.Errorf("volume %s already exists", name)
	}

	if err := os.MkdirAll(volPath, 0755); err != nil {
		return fmt.Errorf("create volume dir: %w", err)
	}

	dataDir := vm.dataPath(name)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create volume data dir: %w", err)
	}

	meta := &VolumeInfo{
		Name:       name,
		Driver:     driver,
		Mountpoint: dataDir,
		CreatedAt:  time.Now(),
	}

	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	if err := os.WriteFile(vm.metadataPath(name), metaData, 0644); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}

	return nil
}

func (vm *VolumeManager) Remove(name string) error {
	volPath := vm.volumePath(name)
	if _, err := os.Stat(volPath); os.IsNotExist(err) {
		return fmt.Errorf("volume %s not found", name)
	}

	return os.RemoveAll(volPath)
}

func (vm *VolumeManager) List() ([]*VolumeInfo, error) {
	entries, err := os.ReadDir(vm.volumesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var volumes []*VolumeInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(vm.volumesDir, e.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var vol VolumeInfo
		if err := json.Unmarshal(data, &vol); err != nil {
			continue
		}

		vol.Size = vm.calculateSize(filepath.Join(vm.volumesDir, e.Name(), "data"))
		volumes = append(volumes, &vol)
	}

	return volumes, nil
}

func (vm *VolumeManager) Inspect(name string) (*VolumeInfo, error) {
	metaPath := vm.metadataPath(name)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("volume %s not found", name)
	}

	var vol VolumeInfo
	if err := json.Unmarshal(data, &vol); err != nil {
		return nil, fmt.Errorf("parse volume metadata: %w", err)
	}

	vol.Size = vm.calculateSize(vm.dataPath(name))
	return &vol, nil
}

func (vm *VolumeManager) Mount(name, containerPath string) (string, error) {
	volPath := vm.dataPath(name)
	if _, err := os.Stat(volPath); os.IsNotExist(err) {
		return "", fmt.Errorf("volume %s not found", name)
	}

	return volPath, nil
}

func (vm *VolumeManager) Unmount(name, containerPath string) error {
	_ = containerPath
	if _, err := os.Stat(vm.dataPath(name)); os.IsNotExist(err) {
		return fmt.Errorf("volume %s not found", name)
	}
	return nil
}

func (vm *VolumeManager) calculateSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}
