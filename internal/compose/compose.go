//go:build linux

package compose

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/container"
)

type Stack struct {
	Name      string    `json:"name"`
	Services  []Service `json:"services"`
	Networks  []Network `json:"networks,omitempty"`
	Volumes   []Volume  `json:"volumes,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Ports        []PortMapping     `json:"ports,omitempty"`
	Volumes      []VolumeMount     `json:"volumes,omitempty"`
	Environment  map[string]string `json:"environment,omitempty"`
	Replicas     int               `json:"replicas"`
	CPUQuota     int64             `json:"cpu_quota,omitempty"`
	MemLimit     int64             `json:"mem_limit,omitempty"`
	DependsOn    []string          `json:"depends_on,omitempty"`
	Status       string            `json:"status"`
	ContainerIDs []string          `json:"container_ids,omitempty"`
}

type PortMapping struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

type Network struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

type Volume struct {
	Name   string `json:"name"`
	Driver string `json:"driver"`
}

type StackManager struct {
	stacksDir string
	cfg       *config.Config
}

func NewStackManager(cfg *config.Config) *StackManager {
	stacksDir := filepath.Join(cfg.RootDir, "stacks")
	os.MkdirAll(stacksDir, 0755)
	return &StackManager{
		stacksDir: stacksDir,
		cfg:       cfg,
	}
}

func (sm *StackManager) LoadStack(path string) (*Stack, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read stack file: %w", err)
	}

	stack := &Stack{}
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, stack); err != nil {
			return nil, fmt.Errorf("parse JSON stack: %w", err)
		}
	case ".yaml", ".yml":
		if err := parseYAMLStack(data, stack); err != nil {
			return nil, fmt.Errorf("parse YAML stack: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}

	if stack.Name == "" {
		stack.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if stack.CreatedAt.IsZero() {
		stack.CreatedAt = time.Now()
	}

	return stack, nil
}

func parseYAMLStack(data []byte, stack *Stack) error {
	lines := strings.Split(string(data), "\n")
	currentSection := ""
	currentService := -1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				currentSection = strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				switch currentSection {
				case "name":
					stack.Name = value
				}
			}
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch currentSection {
		case "services":
			if key != "" && currentService == -1 {
				stack.Services = append(stack.Services, Service{Name: key, Replicas: 1, Status: "stopped"})
				currentService = len(stack.Services) - 1
			} else if currentService >= 0 {
				svc := &stack.Services[currentService]
				switch key {
				case "image":
					svc.Image = value
				case "replicas":
					fmt.Sscanf(value, "%d", &svc.Replicas)
				case "cpu_quota":
					fmt.Sscanf(value, "%d", &svc.CPUQuota)
				case "mem_limit":
					fmt.Sscanf(value, "%d", &svc.MemLimit)
				}
			}
		}
	}
	return nil
}

func (sm *StackManager) DeployStack(stack *Stack) error {
	depOrder := resolveDependencyOrder(stack.Services)

	for _, svcName := range depOrder {
		svc := findService(stack.Services, svcName)
		if svc == nil {
			continue
		}

		for i := 0; i < svc.Replicas; i++ {
			containerName := fmt.Sprintf("%s-%s-%d", stack.Name, svc.Name, i)
			cont, err := container.Create(
				containerName,
				svc.Image,
				svc.Command,
				svc.CPUQuota,
				svc.MemLimit,
				*sm.cfg,
			)
			if err != nil {
				return fmt.Errorf("create service %s replica %d: %w", svc.Name, i, err)
			}

			svc.ContainerIDs = append(svc.ContainerIDs, cont.ID)

			if err := container.Start(cont, *sm.cfg, false); err != nil {
				return fmt.Errorf("start service %s replica %d: %w", svc.Name, i, err)
			}

			svc.Status = "running"
		}
	}

	stack.Status = "running"
	return sm.saveStackState(stack)
}

func (sm *StackManager) StopStack(name string) error {
	stack, err := sm.GetStack(name)
	if err != nil {
		return err
	}

	depOrder := resolveDependencyOrder(stack.Services)
	for i := len(depOrder) - 1; i >= 0; i-- {
		svc := findService(stack.Services, depOrder[i])
		if svc == nil {
			continue
		}

		for _, cid := range svc.ContainerIDs {
			if err := container.Stop(cid, *sm.cfg); err != nil {
				fmt.Printf("warning: stop container %s: %v\n", cid[:12], err)
			}
		}
		svc.Status = "stopped"
		svc.ContainerIDs = nil
	}

	stack.Status = "stopped"
	return sm.saveStackState(stack)
}

func (sm *StackManager) RemoveStack(name string) error {
	stackDir := filepath.Join(sm.stacksDir, name)
	if _, err := os.Stat(stackDir); os.IsNotExist(err) {
		return fmt.Errorf("stack %s not found", name)
	}

	stack, err := sm.GetStack(name)
	if err == nil {
		_ = sm.StopStack(name)

		for _, svc := range stack.Services {
			for _, cid := range svc.ContainerIDs {
				_ = os.RemoveAll(filepath.Join(sm.cfg.ContainerDir, cid))
			}
		}
	}

	return os.RemoveAll(stackDir)
}

func (sm *StackManager) ListStacks() ([]*Stack, error) {
	entries, err := os.ReadDir(sm.stacksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var stacks []*Stack
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		stackFile := filepath.Join(sm.stacksDir, e.Name(), "stack.json")
		stack, err := sm.LoadStack(stackFile)
		if err != nil {
			continue
		}

		stack.Status = sm.computeStackStatus(stack)
		stacks = append(stacks, stack)
	}

	return stacks, nil
}

func (sm *StackManager) GetStack(name string) (*Stack, error) {
	stackFile := filepath.Join(sm.stacksDir, name, "stack.json")
	if _, err := os.Stat(stackFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("stack %s not found", name)
	}
	return sm.LoadStack(stackFile)
}

func (sm *StackManager) GetStackLogs(name string, follow bool) (io.ReadCloser, error) {
	stack, err := sm.GetStack(name)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		for _, svc := range stack.Services {
			for _, cid := range svc.ContainerIDs {
				logDir := filepath.Join(sm.cfg.ContainerDir, cid)
				stdoutPath := filepath.Join(logDir, "stdout.log")
				stderrPath := filepath.Join(logDir, "stderr.log")

				for _, path := range []string{stdoutPath, stderrPath} {
					data, err := os.ReadFile(path)
					if err != nil {
						continue
					}
					if len(data) > 0 {
						header := fmt.Sprintf("[%s/%s] ", name, svc.Name)
						lines := strings.Split(string(data), "\n")
						for _, line := range lines {
							if line != "" {
								fmt.Fprintf(pw, "%s%s\n", header, line)
							}
						}
					}
				}
			}
		}
	}()

	return pr, nil
}

func (sm *StackManager) computeStackStatus(stack *Stack) string {
	running := 0
	total := 0
	for _, svc := range stack.Services {
		total += svc.Replicas
		if svc.Status == "running" {
			running += len(svc.ContainerIDs)
		}
	}

	if running == 0 {
		return "stopped"
	}
	if running == total {
		return "running"
	}
	return "partial"
}

func (sm *StackManager) saveStackState(stack *Stack) error {
	stackDir := filepath.Join(sm.stacksDir, stack.Name)
	if err := os.MkdirAll(stackDir, 0755); err != nil {
		return fmt.Errorf("create stack dir: %w", err)
	}

	data, err := json.MarshalIndent(stack, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stack: %w", err)
	}

	stackFile := filepath.Join(stackDir, "stack.json")
	tmp := stackFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write stack file: %w", err)
	}
	return os.Rename(tmp, stackFile)
}

func resolveDependencyOrder(services []Service) []string {
	graph := make(map[string][]string)
	inDegree := make(map[string]int)

	for _, svc := range services {
		if _, ok := inDegree[svc.Name]; !ok {
			inDegree[svc.Name] = 0
		}
		for _, dep := range svc.DependsOn {
			graph[dep] = append(graph[dep], svc.Name)
			inDegree[svc.Name]++
		}
	}

	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		for _, dependent := range graph[node] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
				sort.Strings(queue)
			}
		}
	}

	if len(result) < len(services) {
		result = nil
		for _, svc := range services {
			result = append(result, svc.Name)
		}
	}

	return result
}

func findService(services []Service, name string) *Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	return nil
}
