//go:build linux

package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Config struct {
	Name      string
	CgroupRoot string
	CPUQuota  int64
	CPUPeriod int64
	MemLimit  int64
	PIDs      int
}

type Stats struct {
	CPUUsage   uint64 `json:"cpu_usage"`
	MemUsage   uint64 `json:"mem_usage"`
	MemMax     uint64 `json:"mem_max"`
	MemFailCnt uint64 `json:"mem_failcnt"`
	OOMKills   uint64 `json:"oom_kills"`
	Processes  int    `json:"processes"`
}

func cgroupPath(cfg Config) string {
	return filepath.Join(cfg.CgroupRoot, "nanocontainer", cfg.Name)
}

func Create(cfg Config) error {
	dir := cgroupPath(cfg)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cgroup: %w", err)
	}
	if cfg.CPUPeriod == 0 {
		cfg.CPUPeriod = 100000
	}
	if cfg.CPUQuota > 0 {
		if err := SetCPU(cfg); err != nil {
			return err
		}
	}
	if cfg.MemLimit > 0 {
		if err := SetMemory(cfg); err != nil {
			return err
		}
	}
	if cfg.PIDs > 0 {
		if err := SetPIDs(cfg); err != nil {
			return err
		}
	}
	return nil
}

func Destroy(cfg Config) error {
	dir := cgroupPath(cfg)
	procs := filepath.Join(dir, "cgroup.procs")
	data, err := os.ReadFile(procs)
	if err == nil {
		pids := strings.Fields(string(data))
		for _, p := range pids {
			pid, err := strconv.Atoi(p)
			if err == nil {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	if err := syscall.Rmdir(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup: %w", err)
	}
	return nil
}

func SetCPU(cfg Config) error {
	dir := cgroupPath(cfg)
	cpuMax := filepath.Join(dir, "cpu.max")
	period := cfg.CPUPeriod
	if period == 0 {
		period = 100000
	}
	quota := cfg.CPUQuota
	if quota <= 0 {
		quota = -1
	}
	val := fmt.Sprintf("%d %d", quota, period)
	return os.WriteFile(cpuMax, []byte(val), 0644)
}

func SetMemory(cfg Config) error {
	dir := cgroupPath(cfg)
	memMax := filepath.Join(dir, "memory.max")
	return os.WriteFile(memMax, []byte(strconv.FormatInt(cfg.MemLimit, 10)), 0644)
}

func SetPIDs(cfg Config) error {
	dir := cgroupPath(cfg)
	pidsMax := filepath.Join(dir, "pids.max")
	return os.WriteFile(pidsMax, []byte(strconv.Itoa(cfg.PIDs)), 0644)
}

func AddProcess(cfg Config, pid int) error {
	dir := cgroupPath(cfg)
	procs := filepath.Join(dir, "cgroup.procs")
	return os.WriteFile(procs, []byte(strconv.Itoa(pid)), 0644)
}

func GetStats(cfg Config) (*Stats, error) {
	dir := cgroupPath(cfg)
	stats := &Stats{}

	cpuStat := filepath.Join(dir, "cpu.stat")
	if data, err := os.ReadFile(cpuStat); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				val, _ := strconv.ParseUint(parts[1], 10, 64)
				switch parts[0] {
				case "usage_usec":
					stats.CPUUsage = val
				}
			}
		}
	}

	memCurrent := filepath.Join(dir, "memory.current")
	if data, err := os.ReadFile(memCurrent); err == nil {
		val, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		stats.MemUsage = val
	}

	memStat := filepath.Join(dir, "memory.stat")
	if data, err := os.ReadFile(memStat); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				val, _ := strconv.ParseUint(parts[1], 10, 64)
				switch parts[0] {
				case "pgmajfault":
					stats.MemFailCnt = val
				case "oom_kill":
					stats.OOMKills = val
				}
			}
		}
	}

	events := filepath.Join(dir, "memory.events")
	if data, err := os.ReadFile(events); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == "oom_kill" {
				val, _ := strconv.ParseUint(parts[1], 10, 64)
				stats.OOMKills = val
			}
		}
	}

	procs := filepath.Join(dir, "cgroup.procs")
	if data, err := os.ReadFile(procs); err == nil {
		stats.Processes = len(strings.Fields(string(data)))
	}

	return stats, nil
}

func IsOOMKilled(cfg Config) (bool, error) {
	stats, err := GetStats(cfg)
	if err != nil {
		return false, err
	}
	return stats.OOMKills > 0, nil
}
