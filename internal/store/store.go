//go:build linux

package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ContainerStatus string

const (
	StatusCreated  ContainerStatus = "created"
	StatusRunning  ContainerStatus = "running"
	StatusStopped  ContainerStatus = "stopped"
)

type ContainerState struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Image        string          `json:"image"`
	Command      []string        `json:"command"`
	Status       ContainerStatus `json:"status"`
	PID          int             `json:"pid"`
	ExitCode     int             `json:"exit_code"`
	CreatedAt    time.Time       `json:"created_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	RootfsPath   string          `json:"rootfs_path"`
	CgroupPath   string          `json:"cgroup_path"`
	Hostname     string          `json:"hostname"`
	NetVethHost  string          `json:"net_veth_host,omitempty"`
	NetVethCont  string          `json:"net_veth_cont,omitempty"`
	NetIP        string          `json:"net_ip,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	CPUQuota     int64           `json:"cpu_quota"`
	MemLimit     int64           `json:"mem_limit"`
}

type Store struct {
	dir   string
	mu    sync.RWMutex
}

func New(dir string) *Store {
	return &Store{dir: dir}
}

func (s *Store) containerDir(id string) string {
	return filepath.Join(s.dir, id)
}

func (s *Store) stateFile(id string) string {
	return filepath.Join(s.containerDir(id), "state.json")
}

func (s *Store) Save(state *ContainerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.containerDir(state.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create container dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmp := s.stateFile(state.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return os.Rename(tmp, s.stateFile(state.ID))
}

func (s *Store) Load(id string) (*ContainerState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.stateFile(id))
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	state := &ContainerState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	return state, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(s.containerDir(id))
}

func (s *Store) List(includeStopped bool) ([]*ContainerState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var states []*ContainerState
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name(), "state.json"))
		if err != nil {
			continue
		}
		var st ContainerState
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		if !includeStopped && st.Status == StatusStopped {
			continue
		}
		states = append(states, &st)
	}
	return states, nil
}
