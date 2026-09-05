//go:build linux

package api

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type SystemStats struct {
	CPU               float64 `json:"cpu_percent"`
	MemoryTotal       uint64  `json:"memory_total_bytes"`
	MemoryUsed        uint64  `json:"memory_used_bytes"`
	MemoryPercent     float64 `json:"memory_percent"`
	DiskTotal         uint64  `json:"disk_total_bytes"`
	DiskUsed          uint64  `json:"disk_used_bytes"`
	DiskPercent       float64 `json:"disk_percent"`
	ContainersTotal   int     `json:"containers_total"`
	ContainersRunning int     `json:"containers_running"`
	ContainersStopped int     `json:"containers_stopped"`
	Uptime            float64 `json:"uptime_seconds"`
}

var startTime = time.Now()

func GetSystemStats() (*SystemStats, error) {
	stats := &SystemStats{}

	cpuPercent, err := readCPUStats()
	if err == nil {
		stats.CPU = cpuPercent
	}

	total, used, err := readMemStats()
	if err == nil {
		stats.MemoryTotal = total
		stats.MemoryUsed = used
		if total > 0 {
			stats.MemoryPercent = float64(used) / float64(total) * 100
		}
	}

	diskTotal, diskUsed, err := readDiskStats()
	if err == nil {
		stats.DiskTotal = diskTotal
		stats.DiskUsed = diskUsed
		if diskTotal > 0 {
			stats.DiskPercent = float64(diskUsed) / float64(diskTotal) * 100
		}
	}

	stats.Uptime = time.Since(startTime).Seconds()

	return stats, nil
}

func readCPUStats() (float64, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, fmt.Errorf("no cpu line in /proc/stat")
	}

	line := scanner.Text()
	if !strings.HasPrefix(line, "cpu ") {
		return 0, fmt.Errorf("unexpected first line: %s", line)
	}

	fields := strings.Fields(line)
	if len(fields) < 5 {
		return 0, fmt.Errorf("insufficient cpu fields")
	}

	var values [10]uint64
	for i := 1; i < len(fields) && i <= 10; i++ {
		values[i-1], _ = strconv.ParseUint(fields[i], 10, 64)
	}

	idle := values[3]
	total := uint64(0)
	for _, v := range values {
		total += v
	}

	time.Sleep(100 * time.Millisecond)

	f2, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer f2.Close()

	scanner2 := bufio.NewScanner(f2)
	if !scanner2.Scan() {
		return 0, fmt.Errorf("no cpu line on second read")
	}

	line2 := scanner2.Text()
	fields2 := strings.Fields(line2)
	if len(fields2) < 5 {
		return 0, fmt.Errorf("insufficient cpu fields on second read")
	}

	var values2 [10]uint64
	for i := 1; i < len(fields2) && i <= 10; i++ {
		values2[i-1], _ = strconv.ParseUint(fields2[i], 10, 64)
	}

	idle2 := values2[3]
	total2 := uint64(0)
	for _, v := range values2 {
		total2 += v
	}

	idleDelta := idle2 - idle
	totalDelta := total2 - total

	if totalDelta == 0 {
		return 0, nil
	}

	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100, nil
}

func readMemStats() (total, used uint64, err error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	memInfo := make(map[string]uint64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		val, _ := strconv.ParseUint(valStr, 10, 64)
		memInfo[key] = val * 1024
	}

	total = memInfo["MemTotal"]
	free := memInfo["MemFree"]
	buffers := memInfo["Buffers"]
	cached := memInfo["Cached"]
	sReclaimable := memInfo["SReclaimable"]

	used = total - free - buffers - cached - sReclaimable
	return total, used, nil
}

func readDiskStats() (total, used uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs: %w", err)
	}

	total = uint64(stat.Blocks) * uint64(stat.Bsize)
	free := uint64(stat.Bfree) * uint64(stat.Bsize)
	used = total - free

	return total, used, nil
}

func FormatPrometheusMetrics(stats *SystemStats, running, total int) string {
	var b strings.Builder

	b.WriteString("# HELP nanocontainer_cpu_percent CPU usage percentage\n")
	b.WriteString("# TYPE nanocontainer_cpu_percent gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_cpu_percent %.2f\n", stats.CPU))

	b.WriteString("# HELP nanocontainer_memory_total_bytes Total memory in bytes\n")
	b.WriteString("# TYPE nanocontainer_memory_total_bytes gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_memory_total_bytes %d\n", stats.MemoryTotal))

	b.WriteString("# HELP nanocontainer_memory_used_bytes Used memory in bytes\n")
	b.WriteString("# TYPE nanocontainer_memory_used_bytes gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_memory_used_bytes %d\n", stats.MemoryUsed))

	b.WriteString("# HELP nanocontainer_memory_percent Memory usage percentage\n")
	b.WriteString("# TYPE nanocontainer_memory_percent gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_memory_percent %.2f\n", stats.MemoryPercent))

	b.WriteString("# HELP nanocontainer_disk_total_bytes Total disk space in bytes\n")
	b.WriteString("# TYPE nanocontainer_disk_total_bytes gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_disk_total_bytes %d\n", stats.DiskTotal))

	b.WriteString("# HELP nanocontainer_disk_used_bytes Used disk space in bytes\n")
	b.WriteString("# TYPE nanocontainer_disk_used_bytes gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_disk_used_bytes %d\n", stats.DiskUsed))

	b.WriteString("# HELP nanocontainer_disk_percent Disk usage percentage\n")
	b.WriteString("# TYPE nanocontainer_disk_percent gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_disk_percent %.2f\n", stats.DiskPercent))

	b.WriteString("# HELP nanocontainer_containers_running Number of running containers\n")
	b.WriteString("# TYPE nanocontainer_containers_running gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_containers_running %d\n", running))

	b.WriteString("# HELP nanocontainer_containers_total Total number of containers\n")
	b.WriteString("# TYPE nanocontainer_containers_total gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_containers_total %d\n", total))

	b.WriteString("# HELP nanocontainer_uptime_seconds System uptime in seconds\n")
	b.WriteString("# TYPE nanocontainer_uptime_seconds gauge\n")
	b.WriteString(fmt.Sprintf("nanocontainer_uptime_seconds %.2f\n", stats.Uptime))

	return b.String()
}
