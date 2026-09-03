//go:build linux

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	RootDir     string `json:"root_dir"`
	StorageDir  string `json:"storage_dir"`
	ContainerDir string `json:"container_dir"`
	ImageDir    string `json:"image_dir"`
	LogDir      string `json:"log_dir"`
	NetworkDir  string `json:"network_dir"`
	CgroupRoot  string `json:"cgroup_root"`
	BridgeName  string `json:"bridge_name"`
	BridgeIP    string `json:"bridge_ip"`
}

var (
	globalConfig *Config
	configOnce   sync.Once
)

func DefaultConfig() *Config {
	return &Config{
		RootDir:     "/var/lib/nanocontainer",
		StorageDir:  "/var/lib/nanocontainer",
		ContainerDir: "/var/lib/nanocontainer/containers",
		ImageDir:    "/var/lib/nanocontainer/images",
		LogDir:      "/var/log/nanocontainer",
		NetworkDir:  "/var/lib/nanocontainer/network",
		CgroupRoot:  "/sys/fs/cgroup",
		BridgeName:  "nanocontainer0",
		BridgeIP:    "10.200.0.1/24",
	}
}

func Get() *Config {
	configOnce.Do(func() {
		globalConfig = DefaultConfig()
	})
	return globalConfig
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			if err := cfg.Save(path); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, err
	}
	cfg := &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	globalConfig = cfg
	return cfg, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (c *Config) EnsureDirs() error {
	dirs := []string{c.ContainerDir, c.ImageDir, c.LogDir, c.NetworkDir}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}
