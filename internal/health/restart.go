//go:build linux

package health

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RestartPolicy struct {
	Name              string `json:"name"` // "no", "always", "unless-stopped", "on-failure"
	MaximumRetryCount int    `json:"maximum_retry_count,omitempty"`
}

type RestartManager struct {
	containerID  string
	policy       *RestartPolicy
	restartCount int
	maxRestarts  int
	lastRestart  time.Time
	stopCh       chan struct{}
	mu           sync.Mutex
	manualStop   bool
}

func NewRestartManager(containerID string, policy *RestartPolicy) *RestartManager {
	if policy == nil {
		policy = &RestartPolicy{Name: "no"}
	}

	maxRestarts := policy.MaximumRetryCount
	if maxRestarts <= 0 && policy.Name == "always" {
		maxRestarts = -1
	}

	return &RestartManager{
		containerID: containerID,
		policy:      policy,
		maxRestarts: maxRestarts,
		stopCh:      make(chan struct{}),
		manualStop:  false,
	}
}

func (rm *RestartManager) ShouldRestart(exitCode int) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch rm.policy.Name {
	case "no":
		return false

	case "always":
		if rm.manualStop {
			return false
		}
		if rm.maxRestarts > 0 && rm.restartCount >= rm.maxRestarts {
			return false
		}
		return true

	case "unless-stopped":
		if rm.manualStop {
			return false
		}
		if rm.maxRestarts > 0 && rm.restartCount >= rm.maxRestarts {
			return false
		}
		return true

	case "on-failure":
		if exitCode == 0 {
			return false
		}
		if rm.maxRestarts > 0 && rm.restartCount >= rm.maxRestarts {
			return false
		}
		return true

	default:
		return false
	}
}

func (rm *RestartManager) Monitor(containerPID int, restartFunc func() error) {
	rm.mu.Lock()
	rm.stopCh = make(chan struct{})
	rm.mu.Unlock()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rm.stopCh:
			return
		case <-ticker.C:
			if containerPID <= 0 {
				continue
			}

			pidExists := false
			pidPath := fmt.Sprintf("/proc/%d/status", containerPID)
			if _, err := os.Stat(pidPath); err == nil {
				pidExists = true
			}

			if !pidExists {
				exitCode := rm.getExitCode(containerPID)

				if rm.ShouldRestart(exitCode) {
					rm.mu.Lock()
					rm.restartCount++
					rm.lastRestart = time.Now()
					rm.mu.Unlock()

					if err := restartFunc(); err != nil {
						fmt.Printf("restart failed for container %s: %v\n", rm.containerID, err)
						continue
					}

					fmt.Printf("container %s restarted (attempt %d)\n", rm.containerID, rm.restartCount)
				} else {
					fmt.Printf("container %s not restarting (policy: %s, exit code: %d)\n",
						rm.containerID, rm.policy.Name, exitCode)
					return
				}
			}
		}
	}
}

func (rm *RestartManager) Stop() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.manualStop = true
	close(rm.stopCh)
}

func (rm *RestartManager) GetRestartCount() int {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.restartCount
}

func (rm *RestartManager) getExitCode(pid int) int {
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return -1
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		if strings.HasPrefix(line, "State:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				state := parts[1]
				switch state {
				case "Z":
					return 137
				case "X", "x":
					return 0
				}
			}
		}
	}

	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	cmdline, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return 0
	}

	_ = cmdline
	codePath := fmt.Sprintf("/proc/%d/wchan", pid)
	codeData, err := os.ReadFile(codePath)
	if err != nil {
		return 0
	}

	codeStr := strings.TrimSpace(string(codeData))
	if codeStr == "" {
		return 0
	}

	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return 0
	}

	return code
}
