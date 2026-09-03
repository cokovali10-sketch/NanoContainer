//go:build linux

package network

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Manager struct {
	NetworkDir string
	BridgeName string
	BridgeIP   string
}

func New(networkDir, bridgeName, bridgeIP string) *Manager {
	return &Manager{
		NetworkDir: networkDir,
		BridgeName: bridgeName,
		BridgeIP:   bridgeIP,
	}
}

type NetConfig struct {
	VethHost  string `json:"veth_host"`
	VethCont  string `json:"veth_cont"`
	ContIP    string `json:"cont_ip"`
	Gateway   string `json:"gateway"`
	Bridge    string `json:"bridge"`
}

func (m *Manager) SetupBridge() error {
	out, err := exec.Command("ip", "link", "show", m.BridgeName).CombinedOutput()
	if err == nil && strings.Contains(string(out), "state UP") {
		return nil
	}

	if err := exec.Command("ip", "link", "add", m.BridgeName, "type", "bridge").Run(); err != nil {
		return fmt.Errorf("create bridge: %w", err)
	}

	if err := exec.Command("ip", "addr", "add", m.BridgeIP, "dev", m.BridgeName).Run(); err != nil {
		return fmt.Errorf("assign bridge IP: %w", err)
	}

	if err := exec.Command("ip", "link", "set", m.BridgeName, "up").Run(); err != nil {
		return fmt.Errorf("up bridge: %w", err)
	}

	if err := exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run(); err != nil {
		return fmt.Errorf("enable IP forwarding: %w", err)
	}

	return nil
}

func (m *Manager) CreateVethPair(containerID string) (*NetConfig, error) {
	vethHost := "veth-h-" + containerID[:8]
	vethCont := "veth-c-" + containerID[:8]

	if err := exec.Command("ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethCont).Run(); err != nil {
		return nil, fmt.Errorf("create veth pair: %w", err)
	}

	if err := exec.Command("ip", "link", "set", vethHost, "up").Run(); err != nil {
		m.cleanupVeth(vethHost, vethCont)
		return nil, fmt.Errorf("up veth host: %w", err)
	}

	if err := exec.Command("ip", "link", "set", vethHost, "master", m.BridgeName).Run(); err != nil {
		m.cleanupVeth(vethHost, vethCont)
		return nil, fmt.Errorf("attach to bridge: %w", err)
	}

	ip := m.allocateIP(containerID)

	gateway := strings.Split(m.BridgeIP, "/")[0]

	return &NetConfig{
		VethHost: vethHost,
		VethCont: vethCont,
		ContIP:   ip + "/24",
		Gateway:  gateway,
		Bridge:   m.BridgeName,
	}, nil
}

func (m *Manager) ConfigureContainer(vethCont, contIP, gateway string, nsPid int) error {
	pidStr := strconv.Itoa(nsPid)
	netnsPath := filepath.Join("/proc", pidStr, "ns", "net")
	if _, err := os.Stat(netnsPath); err != nil {
		return fmt.Errorf("network namespace not ready: %w", err)
	}

	if err := exec.Command("ip", "link", "set", vethCont, "netns", pidStr).Run(); err != nil {
		return fmt.Errorf("move veth to ns: %w", err)
	}

	parts := strings.Split(contIP, "/")
	ip := parts[0]

	if err := exec.Command("nsenter", "--net=/proc/"+pidStr+"/ns/net",
		"ip", "addr", "add", contIP, "dev", vethCont).Run(); err != nil {
		return fmt.Errorf("assign IP in ns: %w", err)
	}

	if err := exec.Command("nsenter", "--net=/proc/"+pidStr+"/ns/net",
		"ip", "link", "set", vethCont, "up").Run(); err != nil {
		return fmt.Errorf("up veth in ns: %w", err)
	}

	if err := exec.Command("nsenter", "--net=/proc/"+pidStr+"/ns/net",
		"ip", "route", "add", "default", "via", gateway).Run(); err != nil {
		return fmt.Errorf("set default route: %w", err)
	}

	_ = ip
	return nil
}

func (m *Manager) SetupNAT(containerIP, gateway string) error {
	return exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", containerIP, "-o", m.BridgeName, "-j", "MASQUERADE").Run()
}

func (m *Manager) CleanupContainer(containerID string) {
	cfgPath := filepath.Join(m.NetworkDir, containerID+".json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var cfg NetConfig
	if err := jsonUnmarshal(data, &cfg); err != nil {
		return
	}

	exec.Command("ip", "link", "del", cfg.VethHost).Run()
	exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-s", cfg.ContIP, "-o", m.BridgeName, "-j", "MASQUERADE").Run()
	os.Remove(cfgPath)
}

func (m *Manager) SaveConfig(containerID string, cfg *NetConfig) error {
	if err := os.MkdirAll(m.NetworkDir, 0755); err != nil {
		return err
	}
	data := fmt.Sprintf(`{"veth_host":"%s","veth_cont":"%s","cont_ip":"%s","gateway":"%s","bridge":"%s"}`,
		cfg.VethHost, cfg.VethCont, cfg.ContIP, cfg.Gateway, cfg.Bridge)
	return os.WriteFile(filepath.Join(m.NetworkDir, containerID+".json"), []byte(data), 0644)
}

func (m *Manager) LoadConfig(containerID string) (*NetConfig, error) {
	data, err := os.ReadFile(filepath.Join(m.NetworkDir, containerID+".json"))
	if err != nil {
		return nil, err
	}
	cfg := &NetConfig{}
	if err := jsonUnmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (m *Manager) allocateIP(containerID string) string {
	entries, _ := os.ReadDir(m.NetworkDir)
	used := make(map[int]bool)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(m.NetworkDir, e.Name()))
		s := string(data)
		for i := 2; i < 254; i++ {
			if strings.Contains(s, fmt.Sprintf("10.200.0.%d", i)) {
				used[i] = true
			}
		}
	}
	for i := 2; i < 254; i++ {
		if !used[i] {
			return fmt.Sprintf("10.200.0.%d", i)
		}
	}
	return "10.200.0.100"
}

func (m *Manager) cleanupVeth(host, cont string) {
	exec.Command("ip", "link", "del", host).Run()
	exec.Command("ip", "link", "del", cont).Run()
}

func jsonUnmarshal(data []byte, v interface{}) error {
	s := string(data)
	type jsonLike interface {
		SetField(key, val string)
	}
	fields := map[string]*string{}
	switch t := v.(type) {
	case *NetConfig:
		fields["veth_host"] = &t.VethHost
		fields["veth_cont"] = &t.VethCont
		fields["cont_ip"] = &t.ContIP
		fields["gateway"] = &t.Gateway
		fields["bridge"] = &t.Bridge
	}
	for key, ptr := range fields {
		needle := `"` + key + `":`
		idx := strings.Index(s, needle)
		if idx < 0 {
			continue
		}
		rest := s[idx+len(needle):]
		rest = strings.TrimSpace(rest)
		if len(rest) < 2 {
			continue
		}
		if rest[0] == '"' {
			end := strings.Index(rest[1:], `"`)
			if end >= 0 {
				*ptr = rest[1 : end+1]
			}
		}
	}
	return nil
}
