//go:build linux

package exec

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type ExecOptions struct {
	ContainerID string
	Command     []string
	WorkingDir  string
	Environment []string
	TTY         bool
	Detach      bool
	User        string
}

type ExecResult struct {
	ExitCode int
	Output   string
	Error    error
}

func loadContainerState(containerID string, cfg config.Config) (*store.ContainerState, error) {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(containerID)
	if err != nil {
		return nil, fmt.Errorf("load container state: %w", err)
	}
	if state.Status != store.StatusRunning {
		return nil, fmt.Errorf("container %s is not running (status: %s)", containerID, state.Status)
	}
	if state.PID <= 0 {
		return nil, fmt.Errorf("container %s has invalid PID: %d", containerID, state.PID)
	}
	procPath := filepath.Join("/proc", strconv.Itoa(state.PID))
	if _, err := os.Stat(procPath); err != nil {
		return nil, fmt.Errorf("container %s process (PID %d) not found: %w", containerID, state.PID, err)
	}
	return state, nil
}

func buildNsenterArgs(pid int, command []string, workDir string, env []string, user string) []string {
	pidStr := strconv.Itoa(pid)
	args := []string{
		"--target", pidStr,
		"--pid",
		"--mount",
		"--net",
		"--uts",
	}
	if user != "" {
		args = append(args, "--user="+user)
	}
	if workDir != "" {
		args = append(args, "--wd="+workDir)
	}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	args = append(args, "--")
	args = append(args, command...)
	return args
}

func ExecIntoContainer(opts ExecOptions, cfg config.Config) (*ExecResult, error) {
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	state, err := loadContainerState(opts.ContainerID, cfg)
	if err != nil {
		return nil, err
	}
	command := resolveCommand(state.RootfsPath, opts.Command)
	nsenterArgs := buildNsenterArgs(state.PID, command, opts.WorkingDir, opts.Environment, opts.User)
	cmd := exec.Command("nsenter", nsenterArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
		return &ExecResult{
			ExitCode: exitCode,
			Output:   stdout.String() + stderr.String(),
			Error:    err,
		}, nil
	}
	return &ExecResult{
		ExitCode: 0,
		Output:   stdout.String(),
		Error:    nil,
	}, nil
}

func ExecAttach(opts ExecOptions, cfg config.Config) error {
	if len(opts.Command) == 0 {
		return fmt.Errorf("command is required")
	}
	state, err := loadContainerState(opts.ContainerID, cfg)
	if err != nil {
		return err
	}
	command := resolveCommand(state.RootfsPath, opts.Command)
	nsenterArgs := buildNsenterArgs(state.PID, command, opts.WorkingDir, opts.Environment, opts.User)
	cmd := exec.Command("nsenter", nsenterArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if opts.TTY {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setctty: true,
			Ctty:    0,
			Setsid:  true,
		}
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	return nil
}

func ExecDetached(opts ExecOptions, cfg config.Config) (*ExecResult, error) {
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}
	state, err := loadContainerState(opts.ContainerID, cfg)
	if err != nil {
		return nil, err
	}
	command := resolveCommand(state.RootfsPath, opts.Command)
	nsenterArgs := buildNsenterArgs(state.PID, command, opts.WorkingDir, opts.Environment, opts.User)
	cmd := exec.Command("nsenter", nsenterArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setpgid: true,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start detached exec: %w", err)
	}
	go func() {
		_ = cmd.Wait()
	}()
	return &ExecResult{
		ExitCode: 0,
		Output:   "",
		Error:    nil,
	}, nil
}

func resolveCommand(rootfsPath string, command []string) []string {
	resolved := make([]string, len(command))
	for i, part := range command {
		if i == 0 {
			binPath := filepath.Join(rootfsPath, strings.TrimPrefix(part, "/"))
			if info, err := os.Stat(binPath); err == nil && info.Mode().IsRegular() {
				resolved[i] = binPath
				continue
			}
		}
		resolved[i] = part
	}
	return resolved
}

func ExecWithOutput(containerID string, command []string, cfg config.Config) (string, int, error) {
	result, err := ExecIntoContainer(ExecOptions{
		ContainerID: containerID,
		Command:     command,
	}, cfg)
	if err != nil {
		return "", -1, err
	}
	return result.Output, result.ExitCode, result.Error
}

func GetContainerPID(containerID string, cfg config.Config) (int, error) {
	state, err := loadContainerState(containerID, cfg)
	if err != nil {
		return -1, err
	}
	return state.PID, nil
}

func IsProcessRunning(pid int) bool {
	procPath := filepath.Join("/proc", strconv.Itoa(pid))
	_, err := os.Stat(procPath)
	return err == nil
}

func KillContainerProcesses(containerID string, cfg config.Config) error {
	state, err := loadContainerState(containerID, cfg)
	if err != nil {
		return err
	}
	procsFile := filepath.Join(cfg.CgroupRoot, "nanocontainer", containerID, "cgroup.procs")
	data, err := os.ReadFile(procsFile)
	if err != nil {
		return syscall.Kill(state.PID, syscall.SIGKILL)
	}
	pids := strings.Fields(string(data))
	for _, p := range pids {
		pid, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		if pid == state.PID {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return syscall.Kill(state.PID, syscall.SIGKILL)
}
