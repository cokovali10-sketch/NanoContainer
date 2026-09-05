//go:build linux

package inspect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type ContainerInfo struct {
	ID              string            `json:"Id"`
	Name            string            `json:"Name"`
	Image           string            `json:"Image"`
	Command         []string          `json:"Command"`
	Args            []string          `json:"Args"`
	State           string            `json:"State"`
	PID             int               `json:"Pid"`
	ExitCode        int               `json:"ExitCode"`
	Created         string            `json:"Created"`
	Started         string            `json:"Started"`
	Finished        string            `json:"Finished"`
	RestartCount    int               `json:"RestartCount"`
	HostConfig      *HostConfig       `json:"HostConfig"`
	Mounts          []MountInfo       `json:"Mounts"`
	NetworkSettings *NetworkSettings  `json:"NetworkSettings"`
	Config          *ContainerConfig  `json:"Config"`
	GraphDriver     *GraphDriverInfo  `json:"GraphDriver"`
}

type HostConfig struct {
	Binds          []string          `json:"Binds"`
	PortBindings   map[string]string `json:"PortBindings"`
	RestartPolicy  string            `json:"RestartPolicy"`
	CPUQuota       int64             `json:"CpuQuota"`
	Memory         int64             `json:"Memory"`
	Privileged     bool              `json:"Privileged"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	SecurityOpt    []string          `json:"SecurityOpt"`
}

type MountInfo struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

type NetworkSettings struct {
	Bridge       string            `json:"Bridge"`
	IPAddress    string            `json:"IPAddress"`
	Gateway      string            `json:"Gateway"`
	MacAddress   string            `json:"MacAddress"`
	PortBindings map[string]string `json:"PortBindings"`
}

type ContainerConfig struct {
	Hostname   string            `json:"Hostname"`
	Domainname string            `json:"Domainname"`
	WorkingDir string            `json:"WorkingDir"`
	Env        []string          `json:"Env"`
	Labels     map[string]string `json:"Labels"`
}

type GraphDriverInfo struct {
	Name string            `json:"Name"`
	Data map[string]string `json:"Data"`
}

func InspectContainer(containerID string, cfg config.Config) (*ContainerInfo, error) {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return nil, fmt.Errorf("load container state: %w", err)
	}
	info := &ContainerInfo{
		ID:        state.ID,
		Name:      state.Name,
		Image:     state.Image,
		Command:   state.Command,
		State:     string(state.Status),
		PID:       state.PID,
		ExitCode:  state.ExitCode,
		Created:   state.CreatedAt.Format("2006-01-02T15:04:05.000000000Z"),
		HostConfig: &HostConfig{
			CPUQuota: state.CPUQuota,
			Memory:   state.MemLimit,
		},
		Config: &ContainerConfig{
			Hostname: state.Hostname,
			Labels:   state.Labels,
		},
		GraphDriver: &GraphDriverInfo{
			Name: "overlay",
			Data: map[string]string{
				"LowerDir": filepath.Join(cfg.ImageDir, state.Image),
				"UpperDir": filepath.Join(cfg.ContainerDir, state.ID, "upper"),
				"MergedDir": state.RootfsPath,
				"WorkDir":  filepath.Join(cfg.ContainerDir, state.ID, "work"),
			},
		},
	}
	if state.StartedAt != nil {
		info.Started = state.StartedAt.Format("2006-01-02T15:04:05.000000000Z")
	}
	if state.Status == store.StatusStopped {
		info.Finished = info.Started
	}
	info.Mounts = inspectMounts(state.RootfsPath)
	info.NetworkSettings = inspectNetwork(state, cfg)
	return info, nil
}

func InspectImage(imageName string, cfg config.Config) (map[string]interface{}, error) {
	tarPath := filepath.Join(cfg.ImageDir, imageName+".tar.gz")
	if _, err := os.Stat(tarPath); err != nil {
		return nil, fmt.Errorf("image %s not found: %w", imageName, err)
	}
	info := map[string]interface{}{
		"Id":       imageName,
		"Size":     getImageSize(tarPath),
		"Rootfs":   imageName,
		"Created":  getImageModTime(tarPath),
		"GraphDriver": map[string]interface{}{
			"Name": "overlay",
			"Data": map[string]string{
				"LowerDir": tarPath,
			},
		},
	}
	return info, nil
}

func FormatJSON(v interface{}) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal JSON: %w", err)
	}
	return string(data), nil
}

func inspectMounts(rootfsPath string) []MountInfo {
	var mounts []MountInfo
	procMounts, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return mounts
	}
	defer procMounts.Close()
	scanner := bufio.NewScanner(procMounts)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		mountPoint := fields[4]
		if !strings.HasPrefix(mountPoint, rootfsPath) {
			continue
		}
		contPath := strings.TrimPrefix(mountPoint, rootfsPath)
		if contPath == "" {
			contPath = "/"
		}
		fstype := fields[8]
		opts := fields[9]
		rw := !strings.Contains(opts, "ro")
		propagation := ""
		for i := 10; i < len(fields)-1; i++ {
			if fields[i] == "shared:" || fields[i] == "master:" || fields[i] == "private:" {
				propagation = fields[i] + " " + fields[i+1]
				break
			}
		}
		mounts = append(mounts, MountInfo{
			Type:        fstype,
			Source:      mountPoint,
			Destination: contPath,
			RW:          rw,
			Propagation: propagation,
		})
	}
	return mounts
}

func inspectNetwork(state *store.ContainerState, cfg config.Config) *NetworkSettings {
	ns := &NetworkSettings{
		Bridge:      cfg.BridgeName,
		PortBindings: make(map[string]string),
	}
	if state.NetIP != "" {
		ns.IPAddress = strings.Split(state.NetIP, "/")[0]
		parts := strings.Split(cfg.BridgeIP, "/")
		if len(parts) > 0 {
			ns.Gateway = parts[0]
		}
	}
	if state.NetVethHost != "" {
		macPath := filepath.Join("/sys/class/net", state.NetVethHost, "address")
		if data, err := os.ReadFile(macPath); err == nil {
			ns.MacAddress = strings.TrimSpace(string(data))
		}
	}
	return ns
}

func getImageSize(tarPath string) int64 {
	info, err := os.Stat(tarPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

func getImageModTime(tarPath string) string {
	info, err := os.Stat(tarPath)
	if err != nil {
		return ""
	}
	return info.ModTime().Format("2006-01-02T15:04:05.000000000Z")
}
