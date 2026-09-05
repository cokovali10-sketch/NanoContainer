//go:build linux

package container

import (
	"os"
	"testing"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
)

func TestGenerateID(t *testing.T) {
	id := GenerateID()
	if len(id) != 12 {
		t.Errorf("expected 12 chars, got %d", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("unexpected char: %c", c)
		}
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := GenerateID()
		if ids[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestCreate(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root")
	}
	cfg := config.DefaultConfig()
	cfg.ContainerDir = t.TempDir()
	cfg.ImageDir = t.TempDir()

	os.MkdirAll(cfg.ImageDir, 0755)

	_, err := Create("test", "nonexistent", []string{"/bin/sh"}, 0, 0, *cfg)
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
}

func TestListEmpty(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root")
	}
	cfg := config.DefaultConfig()
	cfg.ContainerDir = t.TempDir()

	states, err := List(false, *cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected 0 containers, got %d", len(states))
	}
}

func TestGetNonexistent(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root")
	}
	cfg := config.DefaultConfig()
	cfg.ContainerDir = t.TempDir()

	_, err := Get("nonexistent", *cfg)
	if err == nil {
		t.Error("expected error for nonexistent container")
	}
}
