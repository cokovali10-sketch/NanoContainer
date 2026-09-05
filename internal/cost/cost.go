//go:build linux

package cost

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// CloudCost calculates estimated cloud costs for containers.
type CloudCost struct {
	Provider string  // "aws", "gcp", "azure"
	CPU      float64 // cost per vCPU per hour
	Memory   float64 // cost per GB per hour
	Storage  float64 // cost per GB per month
	Network  float64 // cost per GB transferred
}

var (
	AWS定价   = &CloudCost{Provider: "aws", CPU: 0.0464, Memory: 0.0051, Storage: 0.10, Network: 0.09}
	GCP定价   = &CloudCost{Provider: "gcp", CPU: 0.0414, Memory: 0.0046, Storage: 0.08, Network: 0.12}
	Azure定价 = &CloudCost{Provider: "azure", CPU: 0.0448, Memory: 0.0050, Storage: 0.10, Network: 0.087}
)

var providers = map[string]*CloudCost{
	"aws":   AWS定价,
	"gcp":   GCP定价,
	"azure": Azure定价,
}

type ContainerCost struct {
	ContainerID   string  `json:"container_id"`
	ContainerName string  `json:"container_name"`
	CPU           float64 `json:"cpu"`
	MemoryGB      float64 `json:"memory_gb"`
	StorageGB     float64 `json:"storage_gb"`
	NetworkGB     float64 `json:"network_gb"`
	HourlyCost    float64 `json:"hourly_cost"`
	DailyCost     float64 `json:"daily_cost"`
	MonthlyCost   float64 `json:"monthly_cost"`
}

type CostReport struct {
	Provider     string          `json:"provider"`
	Containers   []ContainerCost `json:"containers"`
	TotalHourly  float64         `json:"total_hourly"`
	TotalDaily   float64         `json:"total_daily"`
	TotalMonthly float64         `json:"total_monthly"`
	GeneratedAt  time.Time       `json:"generated_at"`
}

type ContainerInfo struct {
	ID       string
	Name     string
	CPU      float64
	MemoryGB float64
}

func getProvider(name string) (*CloudCost, error) {
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q; supported: aws, gcp, azure", name)
	}
	return p, nil
}

// EstimateCost estimates cost for a container with the given resource usage.
func EstimateCost(cpuCores float64, memoryGB float64, storageGB float64, provider string) (*ContainerCost, error) {
	p, err := getProvider(provider)
	if err != nil {
		return nil, err
	}

	hourlyCPU := cpuCores * p.CPU
	hourlyMem := memoryGB * p.Memory
	hourlyStorage := (storageGB * p.Storage) / (24 * 30)
	monthlyNetwork := memoryGB * p.Network

	hourlyCost := hourlyCPU + hourlyMem + hourlyStorage + monthlyNetwork
	dailyCost := hourlyCost * 24
	monthlyCost := hourlyCost * 24 * 30

	return &ContainerCost{
		CPU:         cpuCores,
		MemoryGB:    memoryGB,
		StorageGB:   storageGB,
		NetworkGB:   0,
		HourlyCost:  round(hourlyCost),
		DailyCost:   round(dailyCost),
		MonthlyCost: round(monthlyCost),
	}, nil
}

// GenerateReport generates a cost report for all provided containers.
func GenerateReport(containers []ContainerInfo, provider string) (*CostReport, error) {
	p, err := getProvider(provider)
	if err != nil {
		return nil, err
	}

	report := &CostReport{
		Provider:    p.Provider,
		Containers:  make([]ContainerCost, 0, len(containers)),
		GeneratedAt: time.Now(),
	}

	for _, c := range containers {
		hourlyCPU := c.CPU * p.CPU
		hourlyMem := c.MemoryGB * p.Memory
		hourlyCost := hourlyCPU + hourlyMem
		dailyCost := hourlyCost * 24
		monthlyCost := hourlyCost * 24 * 30

		cc := ContainerCost{
			ContainerID:   c.ID,
			ContainerName: c.Name,
			CPU:           c.CPU,
			MemoryGB:      c.MemoryGB,
			HourlyCost:    round(hourlyCost),
			DailyCost:     round(dailyCost),
			MonthlyCost:   round(monthlyCost),
		}

		report.Containers = append(report.Containers, cc)
		report.TotalHourly += cc.HourlyCost
		report.TotalDaily += cc.DailyCost
		report.TotalMonthly += cc.MonthlyCost
	}

	report.TotalHourly = round(report.TotalHourly)
	report.TotalDaily = round(report.TotalDaily)
	report.TotalMonthly = round(report.TotalMonthly)

	return report, nil
}

// CompareProviders compares costs across all supported providers.
func CompareProviders(cpuCores, memoryGB, storageGB float64) map[string]*ContainerCost {
	results := make(map[string]*ContainerCost, len(providers))
	for name := range providers {
		cc, err := EstimateCost(cpuCores, memoryGB, storageGB, name)
		if err != nil {
			continue
		}
		results[name] = cc
	}
	return results
}

// FormatCost formats cost for display.
func FormatCost(cost float64) string {
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	if cost < 1.0 {
		return fmt.Sprintf("$%.3f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

// ListProviders returns the names of all supported cloud providers.
func ListProviders() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}

// SavingsBetween calculates the monthly cost difference between two resource configs.
func SavingsBetween(cpuA, memA, cpuB, memB float64, provider string) (float64, error) {
	p, err := getProvider(provider)
	if err != nil {
		return 0, err
	}

	costA := (cpuA*p.CPU + memA*p.Memory) * 24 * 30
	costB := (cpuB*p.CPU + memB*p.Memory) * 24 * 30

	return round(costA - costB), nil
}

// FormatReport returns a human-readable cost report string.
func FormatReport(report *CostReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Cost Report (%s) - Generated %s\n", report.Provider, report.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("=", 60))

	for _, c := range report.Containers {
		name := c.ContainerName
		if name == "" {
			name = c.ContainerID
		}
		fmt.Fprintf(&sb, "  %-30s  CPU: %.2f  Mem: %.1fGB  %s/hr  %s/mo\n",
			name, c.CPU, c.MemoryGB,
			FormatCost(c.HourlyCost),
			FormatCost(c.MonthlyCost),
		)
	}

	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 60))
	fmt.Fprintf(&sb, "  TOTAL:  %s/hr  |  %s/day  |  %s/mo\n",
		FormatCost(report.TotalHourly),
		FormatCost(report.TotalDaily),
		FormatCost(report.TotalMonthly),
	)
	return sb.String()
}

// CheapestProvider returns the provider with the lowest monthly cost for the given specs.
func CheapestProvider(cpuCores, memoryGB, storageGB float64) (string, float64) {
	compared := CompareProviders(cpuCores, memoryGB, storageGB)
	best := ""
	bestCost := math.MaxFloat64

	for name, cc := range compared {
		if cc.MonthlyCost < bestCost {
			bestCost = cc.MonthlyCost
			best = name
		}
	}
	return best, bestCost
}

func round(v float64) float64 {
	return math.Round(v*10000) / 10000
}
