//go:build linux

package network

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type PortForward struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	ContainerID   string `json:"container_id"`
}

func (m *Manager) portForwardFile(containerID string) string {
	return filepath.Join(m.NetworkDir, containerID+".portforward")
}

func (m *Manager) AddPortForward(containerID string, hostPort, containerPort int, protocol string) error {
	if protocol == "" {
		protocol = "tcp"
	}

	cfg, err := m.LoadConfig(containerID)
	if err != nil {
		return fmt.Errorf("load container network config: %w", err)
	}

	containerIP := strings.Split(cfg.ContIP, "/")[0]

	args := []string{
		"-t", "nat", "-A", "PREROUTING",
		"-p", protocol,
		"--dport", strconv.Itoa(hostPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", containerIP, containerPort),
	}

	if err := exec.Command("iptables", args...).Run(); err != nil {
		return fmt.Errorf("add iptables DNAT rule: %w", err)
	}

	args = []string{
		"-t", "nat", "-A", "POSTROUTING",
		"-p", protocol,
		"-d", containerIP,
		"--dport", strconv.Itoa(containerPort),
		"-j", "MASQUERADE",
	}

	if err := exec.Command("iptables", args...).Run(); err != nil {
		exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", protocol, "--dport", strconv.Itoa(hostPort),
			"-j", "DNAT", "--to-destination",
			fmt.Sprintf("%s:%d", containerIP, containerPort)).Run()
		return fmt.Errorf("add iptables MASQUERADE rule: %w", err)
	}

	args = []string{
		"-A", "FORWARD",
		"-p", protocol,
		"-d", containerIP,
		"--dport", strconv.Itoa(containerPort),
		"-j", "ACCEPT",
	}

	if err := exec.Command("iptables", args...).Run(); err != nil {
		exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
			"-p", protocol, "--dport", strconv.Itoa(hostPort),
			"-j", "DNAT", "--to-destination",
			fmt.Sprintf("%s:%d", containerIP, containerPort)).Run()
		exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
			"-p", protocol, "-d", containerIP, "--dport", strconv.Itoa(containerPort),
			"-j", "MASQUERADE").Run()
		return fmt.Errorf("add iptables FORWARD rule: %w", err)
	}

	pf := PortForward{
		HostPort:      hostPort,
		ContainerPort: containerPort,
		Protocol:      protocol,
		ContainerID:   containerID,
	}

	return m.savePortForward(containerID, &pf)
}

func (m *Manager) RemovePortForward(containerID string, hostPort int, protocol string) error {
	if protocol == "" {
		protocol = "tcp"
	}

	cfg, err := m.LoadConfig(containerID)
	if err != nil {
		return fmt.Errorf("load container network config: %w", err)
	}

	containerIP := strings.Split(cfg.ContIP, "/")[0]

	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING",
		"-p", protocol, "--dport", strconv.Itoa(hostPort),
		"-j", "DNAT", "--to-destination",
		fmt.Sprintf("%s:*", containerIP)).Run()

	exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-p", protocol, "-d", containerIP,
		"-j", "MASQUERADE").Run()

	exec.Command("iptables", "-D", "FORWARD",
		"-p", protocol,
		"-d", containerIP,
		"-j", "ACCEPT").Run()

	return m.removePortForwardByPort(containerID, hostPort, protocol)
}

func (m *Manager) ListPortForwards(containerID string) ([]PortForward, error) {
	filePath := m.portForwardFile(containerID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var forwards []PortForward
	if err := json.Unmarshal(data, &forwards); err != nil {
		return nil, fmt.Errorf("parse port forwards: %w", err)
	}

	return forwards, nil
}

func (m *Manager) savePortForward(containerID string, pf *PortForward) error {
	forwards, _ := m.ListPortForwards(containerID)

	for _, existing := range forwards {
		if existing.HostPort == pf.HostPort && existing.Protocol == pf.Protocol {
			return fmt.Errorf("port forward %d/%s already exists", pf.HostPort, pf.Protocol)
		}
	}

	forwards = append(forwards, *pf)

	data, err := json.MarshalIndent(forwards, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal port forwards: %w", err)
	}

	return os.WriteFile(m.portForwardFile(containerID), data, 0644)
}

func (m *Manager) removePortForwardByPort(containerID string, hostPort int, protocol string) error {
	forwards, err := m.ListPortForwards(containerID)
	if err != nil {
		return err
	}

	var updated []PortForward
	for _, pf := range forwards {
		if pf.HostPort != hostPort || pf.Protocol != protocol {
			updated = append(updated, pf)
		}
	}

	data, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal port forwards: %w", err)
	}

	return os.WriteFile(m.portForwardFile(containerID), data, 0644)
}

func (m *Manager) CleanupPortForwards(containerID string) {
	forwards, _ := m.ListPortForwards(containerID)
	for _, pf := range forwards {
		_ = m.RemovePortForward(containerID, pf.HostPort, pf.Protocol)
	}
	os.Remove(m.portForwardFile(containerID))
}
