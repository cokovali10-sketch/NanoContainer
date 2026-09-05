//go:build linux

package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RootDir != "/var/lib/nanocontainer" {
		t.Errorf("unexpected root dir: %s", cfg.RootDir)
	}
	if cfg.BridgeName != "nanocontainer0" {
		t.Errorf("unexpected bridge name: %s", cfg.BridgeName)
	}
}

func TestSaveLoad(t *testing.T) {
	tmp := t.TempDir() + "/test-config.json"
	cfg := DefaultConfig()
	cfg.RootDir = "/tmp/test"

	if err := cfg.Save(tmp); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.RootDir != "/tmp/test" {
		t.Errorf("got root dir %s, want /tmp/test", loaded.RootDir)
	}
}

func TestEnsureDirs(t *testing.T) {
	tmp := t.TempDir()
	cfg := DefaultConfig()
	cfg.ContainerDir = tmp + "/containers"
	cfg.ImageDir = tmp + "/images"
	cfg.LogDir = tmp + "/logs"
	cfg.NetworkDir = tmp + "/network"

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}

	for _, d := range []string{cfg.ContainerDir, cfg.ImageDir, cfg.LogDir, cfg.NetworkDir} {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("directory not created: %s", d)
		}
	}
}

func TestSingleton(t *testing.T) {
	cfg1 := Get()
	cfg2 := Get()
	if cfg1 != cfg2 {
		t.Error("Get() should return same pointer")
	}
}
