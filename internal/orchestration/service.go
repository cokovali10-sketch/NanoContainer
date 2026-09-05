//go:build linux

package orchestration

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/container"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type Service struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Replicas     int               `json:"replicas"`
	Port         int               `json:"port,omitempty"`
	Environment  map[string]string `json:"environment,omitempty"`
	Volumes      []string          `json:"volumes,omitempty"`
	Network      string            `json:"network,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Status       string            `json:"status"`
	ContainerIDs []string          `json:"container_ids"`
	CreatedAt    time.Time         `json:"created_at"`
}

type ServiceManager struct {
	services   map[string]*Service
	containers map[string]*container.Container
	mu         sync.RWMutex
	cfg        *config.Config
	stateStore *store.Store
}

func NewServiceManager(cfg *config.Config) *ServiceManager {
	return &ServiceManager{
		services:   make(map[string]*Service),
		containers: make(map[string]*container.Container),
		cfg:        cfg,
		stateStore: store.New(cfg.ContainerDir),
	}
}

func (sm *ServiceManager) CreateService(svc *Service) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.services[svc.Name]; exists {
		return fmt.Errorf("service %s already exists", svc.Name)
	}

	if svc.Replicas <= 0 {
		svc.Replicas = 1
	}

	svc.Status = "creating"
	svc.CreatedAt = time.Now()
	svc.ContainerIDs = make([]string, 0, svc.Replicas)

	for i := 0; i < svc.Replicas; i++ {
		c, err := sm.createReplica(svc, i)
		if err != nil {
			sm.cleanupReplicas(svc.ContainerIDs)
			return fmt.Errorf("create replica %d: %w", i, err)
		}
		svc.ContainerIDs = append(svc.ContainerIDs, c.ID)
		sm.containers[c.ID] = c
	}

	svc.Status = "running"
	sm.services[svc.Name] = svc
	return nil
}

func (sm *ServiceManager) createReplica(svc *Service, index int) (*container.Container, error) {
	name := fmt.Sprintf("%s-%d-%s", svc.Name, index, container.GenerateID()[:6])

	c, err := container.Create(name, svc.Image, svc.Command, 0, 0, *sm.cfg)
	if err != nil {
		return nil, err
	}

	if err := container.Start(c, *sm.cfg, false); err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}

	return c, nil
}

func (sm *ServiceManager) cleanupReplicas(ids []string) {
	for _, id := range ids {
		if c, ok := sm.containers[id]; ok {
			_ = container.Stop(c.ID, *sm.cfg)
			delete(sm.containers, id)
		}
	}
}

func (sm *ServiceManager) DeleteService(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	svc, exists := sm.services[name]
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	svc.Status = "deleting"

	for _, id := range svc.ContainerIDs {
		if c, ok := sm.containers[id]; ok {
			_ = container.Stop(c.ID, *sm.cfg)
			delete(sm.containers, id)
		}
	}

	delete(sm.services, name)
	return nil
}

func (sm *ServiceManager) ScaleService(name string, replicas int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	svc, exists := sm.services[name]
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if replicas <= 0 {
		return fmt.Errorf("replica count must be positive")
	}

	current := len(svc.ContainerIDs)

	if replicas > current {
		for i := current; i < replicas; i++ {
			c, err := sm.createReplica(svc, i)
			if err != nil {
				return fmt.Errorf("create replica %d: %w", i, err)
			}
			svc.ContainerIDs = append(svc.ContainerIDs, c.ID)
			sm.containers[c.ID] = c
		}
	} else if replicas < current {
		idsToRemove := svc.ContainerIDs[replicas:]
		for _, id := range idsToRemove {
			if c, ok := sm.containers[id]; ok {
				_ = container.Stop(c.ID, *sm.cfg)
				delete(sm.containers, id)
			}
		}
		svc.ContainerIDs = svc.ContainerIDs[:replicas]
	}

	svc.Replicas = replicas
	return nil
}

func (sm *ServiceManager) UpdateService(name, newImage string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	svc, exists := sm.services[name]
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	svc.Status = "updating"
	oldImage := svc.Image
	svc.Image = newImage

	newContainerIDs := make([]string, 0, len(svc.ContainerIDs))

	for i, oldID := range svc.ContainerIDs {
		newC, err := sm.createReplica(svc, i)
		if err != nil {
			svc.Image = oldImage
			svc.Status = "running"
			sm.cleanupReplicas(newContainerIDs)
			return fmt.Errorf("create new replica: %w", err)
		}

		time.Sleep(2 * time.Second)

		if !sm.isContainerHealthy(newC) {
			svc.Image = oldImage
			svc.Status = "running"
			_ = container.Stop(newC.ID, *sm.cfg)
			sm.cleanupReplicas(newContainerIDs)
			return fmt.Errorf("new replica %s failed health check", newC.ID[:12])
		}

		if oldC, ok := sm.containers[oldID]; ok {
			_ = container.Stop(oldC.ID, *sm.cfg)
			delete(sm.containers, oldID)
		}

		newContainerIDs = append(newContainerIDs, newC.ID)
		sm.containers[newC.ID] = newC
	}

	svc.ContainerIDs = newContainerIDs
	svc.Status = "running"
	return nil
}

func (sm *ServiceManager) isContainerHealthy(c *container.Container) bool {
	stateStore := store.New(sm.cfg.ContainerDir)
	state, err := stateStore.Load(c.ID)
	if err != nil {
		return false
	}
	return state.Status == store.StatusRunning
}

func (sm *ServiceManager) GetService(name string) (*Service, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	svc, exists := sm.services[name]
	if !exists {
		return nil, fmt.Errorf("service %s not found", name)
	}

	result := *svc
	result.ContainerIDs = make([]string, len(svc.ContainerIDs))
	copy(result.ContainerIDs, svc.ContainerIDs)
	return &result, nil
}

func (sm *ServiceManager) ListServices() []*Service {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	result := make([]*Service, 0, len(sm.services))
	for _, svc := range sm.services {
		copy := *svc
		copy.ContainerIDs = make([]string, len(svc.ContainerIDs))
		copy.ContainerIDs = append(copy.ContainerIDs, svc.ContainerIDs...)
		result = append(result, &copy)
	}
	return result
}

func (sm *ServiceManager) GetServiceLogs(name string) (string, error) {
	sm.mu.RLock()
	svc, exists := sm.services[name]
	if !exists {
		sm.mu.RUnlock()
		return "", fmt.Errorf("service %s not found", name)
	}
	ids := make([]string, len(svc.ContainerIDs))
	copy(ids, svc.ContainerIDs)
	sm.mu.RUnlock()

	var sb strings.Builder
	for _, id := range ids {
		logDir := filepath.Join(sm.cfg.ContainerDir, id)
		stdoutPath := filepath.Join(logDir, "stdout.log")
		stderrPath := filepath.Join(logDir, "stderr.log")

		sb.WriteString(fmt.Sprintf("--- Container %s ---\n", id[:12]))

		if data, err := os.ReadFile(stdoutPath); err == nil && len(data) > 0 {
			sb.Write(data)
		}
		if data, err := os.ReadFile(stderrPath); err == nil && len(data) > 0 {
			sb.Write(data)
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func (sm *ServiceManager) LoadBalance(name string) (*container.Container, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if _, exists := sm.services[name]; !exists {
		return nil, fmt.Errorf("service %s not found", name)
	}

	healthy := sm.getHealthyReplicasLocked(name)
	if len(healthy) == 0 {
		return nil, fmt.Errorf("no healthy replicas for service %s", name)
	}

	idx := rand.Intn(len(healthy))
	id := healthy[idx]

	if c, ok := sm.containers[id]; ok {
		return c, nil
	}

	return nil, fmt.Errorf("container %s not found", id)
}

func (sm *ServiceManager) HealthCheckAll() {
	sm.mu.RLock()
	services := make([]*Service, 0, len(sm.services))
	for _, svc := range sm.services {
		services = append(services, svc)
	}
	sm.mu.RUnlock()

	for _, svc := range services {
		sm.mu.Lock()
		healthy := 0
		unhealthy := 0
		for _, id := range svc.ContainerIDs {
			if c, ok := sm.containers[id]; ok {
				if sm.isContainerHealthy(c) {
					healthy++
				} else {
					unhealthy++
				}
			}
		}

		if unhealthy > 0 && healthy == 0 {
			svc.Status = "unhealthy"
		} else if unhealthy > 0 {
			svc.Status = "degraded"
		} else {
			svc.Status = "running"
		}
		sm.mu.Unlock()
	}
}

func (sm *ServiceManager) getHealthyReplicas(name string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.getHealthyReplicasLocked(name)
}

func (sm *ServiceManager) getHealthyReplicasLocked(name string) []string {
	svc, exists := sm.services[name]
	if !exists {
		return nil
	}

	var healthy []string
	for _, id := range svc.ContainerIDs {
		if c, ok := sm.containers[id]; ok {
			if sm.isContainerHealthy(c) {
				healthy = append(healthy, id)
			}
		}
	}
	return healthy
}
