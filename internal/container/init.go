//go:build linux

package container

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/cgroup"
	"github.com/cokovali10-sketch/NanoContainer/internal/namespace"
	"github.com/cokovali10-sketch/NanoContainer/internal/rootfs"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

const InitArg = "__nanocontainer_init__"

func isInitProcess() bool {
	return len(os.Args) > 1 && os.Args[1] == InitArg
}

func RunInit() error {
	id := os.Getenv("NC_CONTAINER_ID")
	if id == "" {
		return fmt.Errorf("NC_CONTAINER_ID not set")
	}

	state, err := store.New(config.Get().ContainerDir).Load(id)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	logDir := filepath.Dir(state.RootfsPath)
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open stdout log: %w", err)
	}
	defer stdout.Close()

	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open stderr log: %w", err)
	}
	defer stderr.Close()

	syscall.Dup2(int(stdout.Fd()), 1)
	syscall.Dup2(int(stderr.Fd()), 2)

	rootfsDir := state.RootfsPath

	if err := namespace.MountProc(rootfsDir); err != nil {
		fmt.Fprintf(os.Stderr, "mount /proc: %v\n", err)
	}
	if err := namespace.MountSys(rootfsDir); err != nil {
		fmt.Fprintf(os.Stderr, "mount /sys: %v\n", err)
	}

	mgr := rootfs.New(config.Get().ImageDir, config.Get().ContainerDir)
	if err := mgr.SetupDevNodes(rootfsDir); err != nil {
		fmt.Fprintf(os.Stderr, "setup dev: %v\n", err)
	}

	if err := namespace.SetupHostname(state.Hostname); err != nil {
		fmt.Fprintf(os.Stderr, "set hostname: %v\n", err)
	}

	if err := namespace.PivotRoot(rootfsDir); err != nil {
		fmt.Fprintf(os.Stderr, "pivot_root: %v\n", err)
	}

	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"TERM=xterm-256color",
		"HOSTNAME=" + state.Hostname,
	}

	if len(state.Command) == 0 {
		state.Command = []string{"/bin/sh"}
	}

	binary := state.Command[0]
	args := state.Command[1:]

	binPath, err := exec.LookPath(binary)
	if err != nil {
		binPath = binary
	}

	err = syscall.Exec(binPath, append([]string{binary}, args...), env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exec %s: %v\n", binary, err)
		state.ExitCode = 127
		_ = store.New(config.Get().ContainerDir).Save(state)
		os.Exit(127)
	}
	return nil
}

func StartInNamespace(c *Container, cfg config.Config) error {
	rootfsDir := c.RootfsPath
	hostname := c.Name

	nsCfg := namespace.Config{
		Hostname:    hostname,
		RootfsPath:  rootfsDir,
		EnablePID:   true,
		EnableMount: true,
		EnableUTS:   true,
		EnableNet:   c.NetConfig != nil,
		EnableUser:  false,
	}

	flags := namespace.CloneFlags(nsCfg)

	state := &store.ContainerState{
		ID:         c.ID,
		Name:       c.Name,
		Image:      c.Image,
		Command:    c.Command,
		Status:     store.StatusRunning,
		RootfsPath: rootfsDir,
		Hostname:   hostname,
		CPUQuota:   c.CPUQuota,
		MemLimit:   c.MemLimit,
	}
	if c.NetConfig != nil {
		state.NetVethHost = c.NetConfig.VethHost
		state.NetVethCont = c.NetConfig.VethCont
		state.NetIP = c.NetConfig.ContIP
	}

	stateStore := store.New(cfg.ContainerDir)
	if err := stateStore.Save(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	args := append([]string{os.Args[0], InitArg}, c.Command...)
	env := []string{
		"NC_CONTAINER_ID=" + c.ID,
		"NC_ROOTFS=" + rootfsDir,
		"NC_HOSTNAME=" + hostname,
	}

	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: flags,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("clone: %w", err)
	}

	c.PID = cmd.Process.Pid
	state.PID = c.PID

	if err := stateStore.Save(state); err != nil {
		return fmt.Errorf("update state: %w", err)
	}

	if c.CPUQuota > 0 || c.MemLimit > 0 {
		cgCfg := cgroup.Config{
			Name:      c.ID,
			CgroupRoot: cfg.CgroupRoot,
			CPUQuota:  c.CPUQuota,
			MemLimit:  c.MemLimit,
		}
		if err := cgroup.Create(cgCfg); err != nil {
			return fmt.Errorf("create cgroup: %w", err)
		}
		if err := cgroup.AddProcess(cgCfg, c.PID); err != nil {
			return fmt.Errorf("add to cgroup: %w", err)
		}
		state.CgroupPath = filepath.Join(cfg.CgroupRoot, "nanocontainer", c.ID)
		_ = stateStore.Save(state)
	}

	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release process: %w", err)
	}

	return nil
}

func StopContainer(id string, cfg config.Config) error {
	stateStore := store.New(cfg.ContainerDir)
	state, err := stateStore.Load(id)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if state.Status != store.StatusRunning {
		return fmt.Errorf("container %s is not running", id)
	}

	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			state.Status = store.StatusStopped
			return stateStore.Save(state)
		}
		return fmt.Errorf("send SIGTERM: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		var w syscall.WaitStatus
		pid, err := syscall.Wait4(state.PID, &w, 0, nil)
		if err != nil || pid == 0 {
			done <- nil
			return
		}
		state.ExitCode = w.ExitStatus()
		done <- nil
	}()

	select {
	case <-done:
	case <-make(chan struct{}):
	}

	syscall.Kill(state.PID, syscall.SIGKILL)

	state.Status = store.StatusStopped
	if err := stateStore.Save(state); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	cgCfg := cgroup.Config{
		Name:       id,
		CgroupRoot: cfg.CgroupRoot,
	}
	_ = cgroup.Destroy(cgCfg)

	return nil
}
