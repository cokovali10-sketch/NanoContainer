//go:build linux

package store

import (
	"os"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	state := &ContainerState{
		ID:        "abc123",
		Name:      "test",
		Image:     "alpine",
		Command:   []string{"/bin/sh"},
		Status:    StatusCreated,
		CreatedAt: time.Now(),
	}

	if err := s.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := s.Load("abc123")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.ID != state.ID {
		t.Errorf("ID: got %s, want %s", loaded.ID, state.ID)
	}
	if loaded.Name != state.Name {
		t.Errorf("Name: got %s, want %s", loaded.Name, state.Name)
	}
	if loaded.Image != state.Image {
		t.Errorf("Image: got %s, want %s", loaded.Image, state.Image)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	s.Save(&ContainerState{ID: "c1", Name: "cont1", Status: StatusRunning, CreatedAt: time.Now()})
	s.Save(&ContainerState{ID: "c2", Name: "cont2", Status: StatusStopped, CreatedAt: time.Now()})

	states, err := s.List(false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("expected 1 running, got %d", len(states))
	}

	states, err = s.List(true)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(states) != 2 {
		t.Errorf("expected 2 total, got %d", len(states))
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	s.Save(&ContainerState{ID: "abc", Name: "test", Status: StatusCreated, CreatedAt: time.Now()})

	if err := s.Delete("abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.Load("abc")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestLoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	_, err := s.Load("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent container")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	state := &ContainerState{
		ID:        "test",
		Name:      "test",
		Status:    StatusCreated,
		CreatedAt: time.Now(),
	}

	if err := s.Save(state); err != nil {
		t.Fatalf("save: %v", err)
	}

	files, _ := os.ReadDir(dir + "/test")
	for _, f := range files {
		if f.Name() == "state.json.tmp" {
			t.Error("temp file should not exist after save")
		}
	}
}
