//go:build linux

package stats

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type ContainerStats struct {
	ContainerID   string    `json:"container_id"`
	Timestamp     time.Time `json:"timestamp"`
	CPUUsage      float64   `json:"cpu_usage"`
	MemoryUsage   uint64    `json:"memory_usage"`
	MemoryLimit   uint64    `json:"memory_limit"`
	MemoryPercent float64   `json:"memory_percent"`
	NetworkRx     uint64    `json:"network_rx"`
	NetworkTx     uint64    `json:"network_tx"`
	DiskRead      uint64    `json:"disk_read"`
	DiskWrite     uint64    `json:"disk_write"`
	PIDs          int       `json:"pids"`
	OOMKilled     bool      `json:"oom_killed"`
}

type StatsHistory struct {
	ContainerID string           `json:"container_id"`
	Stats       []ContainerStats `json:"stats"`
	MaxSize     int
	mu          sync.RWMutex
}

func NewStatsHistory(containerID string, maxSize int) *StatsHistory {
	if maxSize <= 0 {
		maxSize = 60
	}
	return &StatsHistory{
		ContainerID: containerID,
		MaxSize:     maxSize,
	}
}

func (h *StatsHistory) Add(s ContainerStats) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Stats = append(h.Stats, s)
	if len(h.Stats) > h.MaxSize {
		h.Stats = h.Stats[len(h.Stats)-h.MaxSize:]
	}
}

func (h *StatsHistory) Latest() *ContainerStats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.Stats) == 0 {
		return nil
	}
	return &h.Stats[len(h.Stats)-1]
}

func GetContainerStats(containerID string, cfg config.Config) (*ContainerStats, error) {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return nil, fmt.Errorf("load container state: %w", err)
	}
	stats := &ContainerStats{
		ContainerID: containerID,
		Timestamp:   time.Now(),
	}
	if state.Status != store.StatusRunning {
		return stats, nil
	}
	cgCfg := filepath.Join(cfg.CgroupRoot, "nanocontainer", containerID)
	stats.CPUUsage = readCPUUsage(cgCfg)
	stats.MemoryUsage, stats.MemoryLimit, stats.MemoryPercent = readMemoryUsage(cgCfg)
	stats.PIDs = readPIDs(cgCfg)
	stats.OOMKilled = readOOMKilled(cgCfg)
	if state.NetVethHost != "" {
		rx, tx, err := GetNetworkStats(state.NetVethHost)
		if err == nil {
			stats.NetworkRx = rx
			stats.NetworkTx = tx
		}
	}
	return stats, nil
}

func StreamStats(containerID string, cfg config.Config, interval time.Duration) (<-chan *ContainerStats, error) {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return nil, fmt.Errorf("load container state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return nil, fmt.Errorf("container %s is not running", containerID)
	}
	ch := make(chan *ContainerStats, 1)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			state, err := stateStore.Load(containerID)
			if err != nil || state.Status != store.StatusRunning {
				return
			}
			stats, err := GetContainerStats(containerID, cfg)
			if err != nil {
				return
			}
			select {
			case ch <- stats:
			default:
			}
		}
	}()
	return ch, nil
}

func GetCPUUsage(containerID string) (float64, error) {
	stateStore := store.New(config.Get().ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return 0, fmt.Errorf("load state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return 0, nil
	}
	cgDir := filepath.Join(config.Get().CgroupRoot, "nanocontainer", containerID)
	return readCPUUsagePercent(cgDir), nil
}

func GetMemoryUsage(containerID string) (uint64, uint64, error) {
	stateStore := store.New(config.Get().ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return 0, 0, fmt.Errorf("load state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return 0, 0, nil
	}
	cgDir := filepath.Join(config.Get().CgroupRoot, "nanocontainer", containerID)
	usage, limit, _ := readMemoryUsage(cgDir)
	return usage, limit, nil
}

func GetNetworkStats(veth string) (rx, tx uint64, err error) {
	rxPath := filepath.Join("/sys/class/net", veth, "statistics", "rx_bytes")
	txPath := filepath.Join("/sys/class/net", veth, "statistics", "tx_bytes")
	rxData, err := os.ReadFile(rxPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read rx_bytes: %w", err)
	}
	txData, err := os.ReadFile(txPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read tx_bytes: %w", err)
	}
	rx, _ = strconv.ParseUint(strings.TrimSpace(string(rxData)), 10, 64)
	tx, _ = strconv.ParseUint(strings.TrimSpace(string(txData)), 10, 64)
	return rx, tx, nil
}

func readCPUUsage(cgDir string) float64 {
	cpuStatPath := filepath.Join(cgDir, "cpu.stat")
	f, err := os.Open(cpuStatPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	usageUs := uint64(0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "usage_usec" {
			usageUs, _ = strconv.ParseUint(parts[1], 10, 64)
			break
		}
	}
	return float64(usageUs) / 10000.0
}

func readCPUUsagePercent(cgDir string) float64 {
	cpuStatPath := filepath.Join(cgDir, "cpu.stat")
	f, err := os.Open(cpuStatPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	usageUs := uint64(0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "usage_usec" {
			usageUs, _ = strconv.ParseUint(parts[1], 10, 64)
			break
		}
	}
	return float64(usageUs) / 10000.0
}

func readMemoryUsage(cgDir string) (usage, limit uint64, percent float64) {
	memCurrentPath := filepath.Join(cgDir, "memory.current")
	data, err := os.ReadFile(memCurrentPath)
	if err == nil {
		usage, _ = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	}
	memMaxPath := filepath.Join(cgDir, "memory.max")
	data, err = os.ReadFile(memMaxPath)
	if err == nil {
		val := strings.TrimSpace(string(data))
		if val == "max" {
			limit = 0
		} else {
			limit, _ = strconv.ParseUint(val, 10, 64)
		}
	}
	if limit > 0 {
		percent = float64(usage) / float64(limit) * 100.0
	}
	return usage, limit, percent
}

func readPIDs(cgDir string) int {
	procsPath := filepath.Join(cgDir, "cgroup.procs")
	data, err := os.ReadFile(procsPath)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(data)))
}

func readOOMKilled(cgDir string) bool {
	eventsPath := filepath.Join(cgDir, "memory.events")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "oom_kill" {
			val, _ := strconv.ParseUint(parts[1], 10, 64)
			return val > 0
		}
	}
	return false
}

func GetDiskStats(containerID string, cfg config.Config) (read, write uint64, err error) {
	state, err := store.New(cfg.ContainerDir).Load(containerID)
	if err != nil {
		return 0, 0, fmt.Errorf("load state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return 0, 0, nil
	}
	ioStatPath := filepath.Join("/proc", strconv.Itoa(state.PID), "io")
	data, err := os.ReadFile(ioStatPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read io stats: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		switch key {
		case "read_bytes":
			read = val
		case "write_bytes":
			write = val
		}
	}
	return read, write, nil
}
