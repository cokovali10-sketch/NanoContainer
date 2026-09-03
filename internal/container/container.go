//go:build linux

package container

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/network"
	"github.com/cokovali10-sketch/NanoContainer/internal/rootfs"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type Container struct {
	ID        string
	Name      string
	Image     string
	Command   []string
	PID       int
	RootfsPath string
	Hostname  string
	CPUQuota  int64
	MemLimit  int64
	NetConfig *network.NetConfig
}

func GenerateID() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	const chars = "0123456789abcdef"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}

func Create(name, image string, command []string, cpuQuota, memLimit int64, cfg config.Config) (*Container, error) {
	if name == "" {
		name = "nc-" + GenerateID()[:8]
	}

	id := GenerateID()
	if err := os.MkdirAll(cfg.ContainerDir, 0755); err != nil {
		return nil, fmt.Errorf("create container dir: %w", err)
	}

	rfs := rootfs.New(cfg.ImageDir, cfg.ContainerDir)
	if !rfs.ImageExists(image) {
		return nil, fmt.Errorf("image %s not found. Pull it first or place a .tar.gz in %s", image, cfg.ImageDir)
	}

	rootfsPath, err := rfs.PrepareRootfs(image, id)
	if err != nil {
		return nil, fmt.Errorf("prepare rootfs: %w", err)
	}

	c := &Container{
		ID:         id,
		Name:       name,
		Image:      image,
		Command:    command,
		RootfsPath: rootfsPath,
		Hostname:   name,
		CPUQuota:   cpuQuota,
		MemLimit:   memLimit,
	}

	fmt.Printf("Container %s created (rootfs: %s)\n", id[:12], rootfsPath)
	return c, nil
}

func Start(c *Container, cfg config.Config, attach bool) error {
	netMgr := network.New(cfg.NetworkDir, cfg.BridgeName, cfg.BridgeIP)
	if err := netMgr.SetupBridge(); err != nil {
		return fmt.Errorf("setup bridge: %w", err)
	}

	netCfg, err := netMgr.CreateVethPair(c.ID)
	if err != nil {
		return fmt.Errorf("create veth: %w", err)
	}
	c.NetConfig = netCfg

	if err := netMgr.SaveConfig(c.ID, netCfg); err != nil {
		return fmt.Errorf("save net config: %w", err)
	}

	if err := StartInNamespace(c, cfg); err != nil {
		netMgr.CleanupContainer(c.ID)
		return fmt.Errorf("start in namespace: %w", err)
	}

	if err := netMgr.ConfigureContainer(netCfg.VethCont, netCfg.ContIP, netCfg.Gateway, c.PID); err != nil {
		fmt.Printf("Warning: network configure failed: %v\n", err)
	}

	if err := netMgr.SetupNAT(netCfg.ContIP, netCfg.Gateway); err != nil {
		fmt.Printf("Warning: NAT setup failed: %v\n", err)
	}

	fmt.Printf("Container %s started (PID: %d)\n", c.ID[:12], c.PID)

	if attach {
		return Attach(c, cfg)
	}

	return nil
}

func Attach(c *Container, cfg config.Config) error {
	logDir := filepath.Join(cfg.ContainerDir, c.ID)
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	cmd := exec.Command("tail", "-f", "-n", "+1", stdoutPath, stderrPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Stop(id string, cfg config.Config) error {
	return StopContainer(id, cfg)
}

func List(all bool, cfg config.Config) ([]*store.ContainerState, error) {
	stateStore := store.New(cfg.ContainerDir)
	return stateStore.List(all)
}

func Get(id string, cfg config.Config) (*store.ContainerState, error) {
	stateStore := store.New(cfg.ContainerDir)
	return stateStore.Load(id)
}

func Logs(id string, cfg config.Config) error {
	logDir := filepath.Join(cfg.ContainerDir, id)
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	if _, err := os.Stat(stdoutPath); err == nil {
		data, err := os.ReadFile(stdoutPath)
		if err == nil && len(data) > 0 {
			fmt.Print(string(data))
		}
	}
	if _, err := os.Stat(stderrPath); err == nil {
		data, err := os.ReadFile(stderrPath)
		if err == nil && len(data) > 0 {
			fmt.Fprint(os.Stderr, string(data))
		}
	}
	return nil
}
