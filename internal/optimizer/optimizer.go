//go:build linux

package optimizer

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type UsageSample struct {
	CPUUsage    float64 `json:"cpu_usage"`
	MemoryUsage uint64  `json:"memory_usage"`
	Timestamp   int64   `json:"timestamp"`
}

type OptimizationResult struct {
	ContainerID       string  `json:"container_id"`
	CurrentCPU        int64   `json:"current_cpu"`
	CurrentMemory     int64   `json:"current_memory"`
	RecommendedCPU    int64   `json:"recommended_cpu"`
	RecommendedMemory int64   `json:"recommended_memory"`
	PotentialSaving   float64 `json:"potential_saving_percent"`
	Reason            string  `json:"reason"`
	Confidence        float64 `json:"confidence"`
}

type ContainerInfo struct {
	ID       string
	Name     string
	CPU      int64
	Memory   int64
	CpuUsage float64
	MemUsage uint64
}

// AIOptimizer suggests optimal resource limits based on usage history.
type AIOptimizer struct {
	mu      sync.RWMutex
	History map[string][]UsageSample
}

// NewAIOptimizer creates a new optimizer.
func NewAIOptimizer() *AIOptimizer {
	return &AIOptimizer{
		History: make(map[string][]UsageSample),
	}
}

// AddSample adds a usage sample for a container.
func (ao *AIOptimizer) AddSample(containerID string, cpu float64, memory uint64) {
	ao.mu.Lock()
	defer ao.mu.Unlock()

	sample := UsageSample{
		CPUUsage:    cpu,
		MemoryUsage: memory,
		Timestamp:   time.Now().Unix(),
	}
	ao.History[containerID] = append(ao.History[containerID], sample)
}

// Analyze analyzes usage patterns and suggests optimizations for a container.
func (ao *AIOptimizer) Analyze(containerID string) *OptimizationResult {
	ao.mu.RLock()
	defer ao.mu.RUnlock()

	samples, ok := ao.History[containerID]
	if !ok || len(samples) == 0 {
		return &OptimizationResult{
			ContainerID: containerID,
			Reason:      "no usage data available",
			Confidence:  0,
		}
	}

	optCPU, optMem := calculateOptimal(samples)

	result := &OptimizationResult{
		ContainerID:       containerID,
		RecommendedCPU:    int64(math.Ceil(optCPU)),
		RecommendedMemory: int64(optMem),
	}

	confidence := calculateConfidence(samples)
	result.Confidence = confidence

	if confidence < 0.3 {
		result.Reason = "insufficient data for reliable recommendation"
	} else if confidence < 0.6 {
		result.Reason = "limited data; recommendation is tentative"
	} else {
		result.Reason = "high-confidence recommendation based on usage patterns"
	}

	return result
}

// AnalyzeAll analyzes all containers and returns optimization results.
func (ao *AIOptimizer) AnalyzeAll(containers []ContainerInfo) []*OptimizationResult {
	results := make([]*OptimizationResult, 0, len(containers))
	for _, c := range containers {
		r := ao.Analyze(c.ID)
		r.CurrentCPU = c.CPU
		r.CurrentMemory = c.Memory

		if r.RecommendedCPU > 0 && r.CurrentCPU > 0 {
			r.PotentialSaving = calculateSavings(
				r.CurrentCPU, r.CurrentMemory,
				r.RecommendedCPU, r.RecommendedMemory,
			)
		}

		results = append(results, r)
	}
	return results
}

// PredictUsage predicts future CPU usage based on historical samples.
func (ao *AIOptimizer) PredictUsage(samples []UsageSample, horizon int) float64 {
	if len(samples) == 0 {
		return 0
	}
	if len(samples) == 1 {
		return samples[0].CPUUsage
	}

	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Timestamp < samples[j].Timestamp
	})

	n := float64(len(samples))
	var sumX, sumY, sumXY, sumX2 float64

	for i, s := range samples {
		x := float64(i)
		y := s.CPUUsage
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return sumY / n
	}

	slope := (n*sumXY - sumX*sumY) / denom
	intercept := (sumY - slope*sumX) / n

	predicted := intercept + slope*float64(len(samples)+horizon-1)
	if predicted < 0 {
		predicted = 0
	}
	if predicted > 100 {
		predicted = 100
	}
	return predicted
}

func calculateOptimal(samples []UsageSample) (float64, uint64) {
	if len(samples) == 0 {
		return 0, 0
	}

	var totalCPU float64
	var maxMem uint64
	var sumMem uint64

	for _, s := range samples {
		totalCPU += s.CPUUsage
		sumMem += s.MemoryUsage
		if s.MemoryUsage > maxMem {
			maxMem = s.MemoryUsage
		}
	}

	avgCPU := totalCPU / float64(len(samples))
	avgMem := sumMem / uint64(len(samples))

	// Use P95 for CPU to avoid occasional spikes causing over-provisioning.
	p95CPU := percentileCPU(samples, 95)

	// Recommended CPU: P95 usage + 20% headroom, minimum 10 millicores.
	optCPU := p95CPU * 1.2
	if optCPU < 0.01 {
		optCPU = 0.01
	}

	// Recommended memory: max observed + 15% headroom, minimum 4 MB.
	optMem := uint64(float64(maxMem) * 1.15)
	if optMem < 4*1024*1024 {
		optMem = 4 * 1024 * 1024
	}

	_ = avgCPU
	_ = avgMem

	return optCPU, optMem
}

func percentileCPU(samples []UsageSample, pct float64) float64 {
	sorted := make([]float64, len(samples))
	for i, s := range samples {
		sorted[i] = s.CPUUsage
	}
	sort.Float64s(sorted)

	idx := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func calculateConfidence(samples []UsageSample) float64 {
	if len(samples) < 3 {
		return float64(len(samples)) / 10.0
	}

	// More samples and lower variance = higher confidence.
	var values []float64
	for _, s := range samples {
		values = append(values, s.CPUUsage)
	}

	variance := computeVariance(values)
	mean := computeMean(values)

	if mean == 0 {
		return 0.5
	}

	// Coefficient of variation: lower is more stable.
	cv := math.Sqrt(variance) / mean
	if cv > 1.0 {
		cv = 1.0
	}

	sampleScore := math.Min(float64(len(samples))/50.0, 1.0)
	stabilityScore := 1.0 - cv

	return round2(sampleScore*0.5 + stabilityScore*0.5)
}

func computeMean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func computeVariance(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	mean := computeMean(vals)
	var sum float64
	for _, v := range vals {
		d := v - mean
		sum += d * d
	}
	return sum / float64(len(vals))
}

func calculateSavings(currentCPU, currentMem, optCPU, optMem int64) float64 {
	if currentCPU == 0 && currentMem == 0 {
		return 0
	}
	currentTotal := float64(currentCPU + currentMem)
	optTotal := float64(optCPU + optMem)
	if currentTotal == 0 {
		return 0
	}
	saving := (currentTotal - optTotal) / currentTotal * 100
	if saving < 0 {
		saving = 0
	}
	return round2(saving)
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// Summary returns a human-readable summary of an optimization result.
func (r *OptimizationResult) Summary() string {
	return fmt.Sprintf(
		"container %s: CPU %d→%d, Memory %d→%d, saving %.1f%% (confidence %.0f%%)",
		r.ContainerID,
		r.CurrentCPU, r.RecommendedCPU,
		r.CurrentMemory, r.RecommendedMemory,
		r.PotentialSaving,
		r.Confidence*100,
	)
}
