//go:build linux

package orchestration

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

type Node struct {
	ID         string           `json:"id"`
	Address    string           `json:"address"`
	Labels     map[string]string `json:"labels"`
	Capacity   ResourceCapacity `json:"capacity"`
	Allocated  ResourceCapacity `json:"allocated"`
	Containers int              `json:"containers"`
}

type ResourceCapacity struct {
	CPU     int64 `json:"cpu"`
	Memory  int64 `json:"memory"`
	Storage int64 `json:"storage"`
}

type Scheduler struct {
	nodes []*Node
	mu    sync.RWMutex
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		nodes: make([]*Node, 0),
	}
}

func (s *Scheduler) AddNode(node *Node) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	s.nodes = append(s.nodes, node)
}

func (s *Scheduler) RemoveNode(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, n := range s.nodes {
		if n.ID == nodeID {
			s.nodes = append(s.nodes[:i], s.nodes[i+1:]...)
			return
		}
	}
}

func (s *Scheduler) Schedule(requirements ResourceCapacity, labels map[string]string) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	type scoredNode struct {
		node  *Node
		score float64
	}

	candidates := make([]scoredNode, 0)
	for _, n := range s.nodes {
		if !s.meetsRequirements(n, requirements) {
			continue
		}
		if !s.matchesLabels(n, labels) {
			continue
		}
		score := s.ScoreNode(n, requirements, labels)
		candidates = append(candidates, scoredNode{node: n, score: score})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no suitable node found for requirements")
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	best := candidates[0].node
	best.Allocated.CPU += requirements.CPU
	best.Allocated.Memory += requirements.Memory
	best.Allocated.Storage += requirements.Storage
	best.Containers++

	return best, nil
}

func (s *Scheduler) meetsRequirements(node *Node, req ResourceCapacity) bool {
	freeCPU := node.Capacity.CPU - node.Allocated.CPU
	freeMem := node.Capacity.Memory - node.Allocated.Memory
	freeStorage := node.Capacity.Storage - node.Allocated.Storage

	return freeCPU >= req.CPU && freeMem >= req.Memory && freeStorage >= req.Storage
}

func (s *Scheduler) matchesLabels(node *Node, labels map[string]string) bool {
	if len(labels) == 0 {
		return true
	}
	for k, v := range labels {
		if nodeVal, ok := node.Labels[k]; !ok || nodeVal != v {
			return false
		}
	}
	return true
}

func (s *Scheduler) BinPack(requirements ResourceCapacity) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	var bestNode *Node
	bestFit := math.MaxFloat64

	for _, n := range s.nodes {
		if !s.meetsRequirements(n, requirements) {
			continue
		}

		utilization := s.nodeUtilization(n, requirements)
		if utilization < bestFit {
			bestFit = utilization
			bestNode = n
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found for bin packing")
	}

	bestNode.Allocated.CPU += requirements.CPU
	bestNode.Allocated.Memory += requirements.Memory
	bestNode.Allocated.Storage += requirements.Storage
	bestNode.Containers++

	return bestNode, nil
}

func (s *Scheduler) nodeUtilization(node *Node, additional ResourceCapacity) float64 {
	totalCPU := float64(node.Capacity.CPU)
	totalMem := float64(node.Capacity.Memory)

	if totalCPU == 0 || totalMem == 0 {
		return 0
	}

	usedCPU := float64(node.Allocated.CPU+additional.CPU) / totalCPU
	usedMem := float64(node.Allocated.Memory+additional.Memory) / totalMem

	return (usedCPU + usedMem) / 2.0
}

func (s *Scheduler) Spread(requirements ResourceCapacity) (*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	var bestNode *Node
	fewestContainers := math.MaxInt64

	for _, n := range s.nodes {
		if !s.meetsRequirements(n, requirements) {
			continue
		}

		if n.Containers < fewestContainers {
			fewestContainers = n.Containers
			bestNode = n
		}
	}

	if bestNode == nil {
		return nil, fmt.Errorf("no suitable node found for spreading")
	}

	bestNode.Allocated.CPU += requirements.CPU
	bestNode.Allocated.Memory += requirements.Memory
	bestNode.Allocated.Storage += requirements.Storage
	bestNode.Containers++

	return bestNode, nil
}

func (s *Scheduler) ScoreNode(node *Node, requirements ResourceCapacity, labels map[string]string) float64 {
	if !s.meetsRequirements(node, requirements) {
		return -1
	}

	freeCPU := float64(node.Capacity.CPU - node.Allocated.CPU)
	freeMem := float64(node.Capacity.Memory - node.Allocated.Memory)
	reqCPU := float64(requirements.CPU)
	reqMem := float64(requirements.Memory)

	cpuScore := 0.0
	if freeCPU > 0 {
		cpuScore = reqCPU / freeCPU
	}

	memScore := 0.0
	if freeMem > 0 {
		memScore = reqMem / freeMem
	}

	resourceScore := (cpuScore + memScore) / 2.0

	loadPenalty := float64(node.Containers) * 0.1

	labelScore := 0.0
	if len(labels) > 0 {
		matchCount := 0
		for k, v := range labels {
			if nodeVal, ok := node.Labels[k]; ok && nodeVal == v {
				matchCount++
			}
		}
		labelScore = float64(matchCount) / float64(len(labels))
	} else {
		labelScore = 1.0
	}

	return (1.0-resourceScore)*0.4 + (1.0-loadPenalty/(loadPenalty+1))*0.3 + labelScore*0.3
}

func (s *Scheduler) GetNodes() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Node, len(s.nodes))
	copy(result, s.nodes)
	return result
}
