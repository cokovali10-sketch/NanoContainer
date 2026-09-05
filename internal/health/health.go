//go:build linux

package health

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type HealthCheck struct {
	Type        string        `json:"type"` // "command", "http", "tcp", "exec"
	Command     []string      `json:"command,omitempty"`
	URL         string        `json:"url,omitempty"`
	Port        int           `json:"port,omitempty"`
	Interval    time.Duration `json:"interval"`
	Timeout     time.Duration `json:"timeout"`
	StartPeriod time.Duration `json:"start_period"`
	Retries     int           `json:"retries"`
}

type HealthStatus struct {
	Status        string      `json:"status"` // "healthy", "unhealthy", "starting"
	FailingStreak int         `json:"failing_streak"`
	LastCheck     time.Time   `json:"last_check"`
	Log           []HealthLog `json:"log"`
}

type HealthLog struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	ExitCode int       `json:"exit_code"`
	Output   string    `json:"output"`
}

func RunHealthCheck(check *HealthCheck) (*HealthStatus, error) {
	status := &HealthStatus{
		Status:    "starting",
		LastCheck: time.Now(),
		Log:       make([]HealthLog, 0),
	}

	if check == nil {
		return status, fmt.Errorf("nil health check")
	}

	var healthy bool
	var output string
	var err error

	switch check.Type {
	case "command", "exec":
		healthy, output, err = checkCommandDirect(check)
	case "http":
		healthy, output, err = checkHTTPDirect(check)
	case "tcp":
		healthy, output, err = checkTCPDirect(check)
	default:
		return status, fmt.Errorf("unknown health check type: %s", check.Type)
	}

	logEntry := HealthLog{
		Start:    status.LastCheck,
		End:      time.Now(),
		ExitCode: 0,
		Output:   output,
	}
	if err != nil {
		logEntry.ExitCode = 1
		status.FailingStreak++
	} else if !healthy {
		logEntry.ExitCode = 1
		status.FailingStreak++
	} else {
		status.FailingStreak = 0
	}

	status.Log = append(status.Log, logEntry)

	if status.FailingStreak == 0 {
		status.Status = "healthy"
	} else {
		status.Status = "unhealthy"
	}

	return status, err
}

func CheckCommand(check *HealthCheck, containerPID int) (bool, string, error) {
	if len(check.Command) == 0 {
		return false, "", fmt.Errorf("empty command")
	}

	nsenterArgs := []string{
		"-t", strconv.Itoa(containerPID),
		"-m", "-u", "-i", "-n", "-p",
		"--",
	}
	nsenterArgs = append(nsenterArgs, check.Command...)

	cmd := exec.Command("nsenter", nsenterArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC | syscall.CLONE_NEWPID | syscall.CLONE_NEWNET,
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(check.Timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return false, "", fmt.Errorf("command timed out after %s", check.Timeout)
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return false, output, fmt.Errorf("command exited with code %d", exitErr.ExitCode())
		}
		return false, output, err
	}

	return true, output, nil
}

func CheckHTTP(check *HealthCheck, containerIP string) (bool, string, error) {
	if check.URL == "" {
		return false, "", fmt.Errorf("empty URL")
	}

	url := check.URL
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = fmt.Sprintf("http://%s:%d%s", containerIP, check.Port, check.URL)
	}

	client := &http.Client{
		Timeout: check.Timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   check.Timeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   check.Timeout,
			ResponseHeaderTimeout: check.Timeout,
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return false, "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return false, "", fmt.Errorf("failed to read response body: %w", err)
	}

	output := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	return healthy, output, nil
}

func CheckTCP(check *HealthCheck, containerIP string) (bool, string, error) {
	if check.Port <= 0 {
		return false, "", fmt.Errorf("invalid port: %d", check.Port)
	}

	addr := fmt.Sprintf("%s:%d", containerIP, check.Port)

	conn, err := net.DialTimeout("tcp", addr, check.Timeout)
	if err != nil {
		return false, "", fmt.Errorf("TCP connection failed: %w", err)
	}
	defer conn.Close()

	output := fmt.Sprintf("TCP connection to %s successful", addr)
	return true, output, nil
}

func checkCommandDirect(check *HealthCheck) (bool, string, error) {
	if len(check.Command) == 0 {
		return false, "", fmt.Errorf("empty command")
	}

	cmd := exec.Command(check.Command[0], check.Command[1:]...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(check.Timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return false, "", fmt.Errorf("command timed out after %s", check.Timeout)
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n" + stderr.String()
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return false, output, fmt.Errorf("command exited with code %d", exitErr.ExitCode())
		}
		return false, output, err
	}

	return true, output, nil
}

func checkHTTPDirect(check *HealthCheck) (bool, string, error) {
	if check.URL == "" {
		return false, "", fmt.Errorf("empty URL")
	}

	client := &http.Client{
		Timeout: check.Timeout,
	}

	resp, err := client.Get(check.URL)
	if err != nil {
		return false, "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return false, "", fmt.Errorf("failed to read response body: %w", err)
	}

	output := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 400
	return healthy, output, nil
}

func checkTCPDirect(check *HealthCheck) (bool, string, error) {
	if check.Port <= 0 {
		return false, "", fmt.Errorf("invalid port: %d", check.Port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", check.Port)
	conn, err := net.DialTimeout("tcp", addr, check.Timeout)
	if err != nil {
		return false, "", fmt.Errorf("TCP connection failed: %w", err)
	}
	defer conn.Close()

	return true, fmt.Sprintf("TCP connection to %s successful", addr), nil
}

func MonitorHealth(containerID string, check *HealthCheck, containerPID int, callback func(HealthStatus)) {
	if check == nil || callback == nil {
		return
	}

	ticker := time.NewTicker(check.Interval)
	defer ticker.Stop()

	startTime := time.Now()
	status := &HealthStatus{
		Status:    "starting",
		LastCheck: time.Now(),
		Log:       make([]HealthLog, 0),
	}

	for range ticker.C {
		if time.Since(startTime) < check.StartPeriod {
			status.Status = "starting"
		}

		var healthy bool
		var output string
		var err error

		switch check.Type {
		case "command", "exec":
			healthy, output, err = CheckCommand(check, containerPID)
		case "http":
			healthy, output, err = CheckHTTP(check, "127.0.0.1")
		case "tcp":
			healthy, output, err = CheckTCP(check, "127.0.0.1")
		}

		logEntry := HealthLog{
			Start:    status.LastCheck,
			End:      time.Now(),
			ExitCode: 0,
			Output:   output,
		}

		if err != nil {
			logEntry.ExitCode = 1
			status.FailingStreak++
		} else if !healthy {
			logEntry.ExitCode = 1
			status.FailingStreak++
		} else {
			status.FailingStreak = 0
		}

		if len(status.Log) >= 5 {
			status.Log = status.Log[1:]
		}
		status.Log = append(status.Log, logEntry)
		status.LastCheck = time.Now()

		if status.FailingStreak == 0 {
			status.Status = "healthy"
		} else if status.FailingStreak >= check.Retries {
			status.Status = "unhealthy"
		} else {
			status.Status = "starting"
		}

		callback(*status)

		if status.Status == "unhealthy" && check.Retries > 0 && status.FailingStreak >= check.Retries {
			logPath := filepath.Join("/var/log/nanocontainer", containerID+"_health.log")
			os.MkdirAll(filepath.Dir(logPath), 0755)
			os.WriteFile(logPath, []byte(fmt.Sprintf("Container %s marked unhealthy at %s\n", containerID, time.Now())), 0644)
		}
	}
}
