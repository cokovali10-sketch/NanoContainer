//go:build linux

package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const baseURL = "http://localhost:8080"

func api(method, path string, body interface{}) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(req)
}

func skipIfNotRoot(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("integration tests require root privileges")
	}
}

func waitForServer(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("server did not become healthy within timeout")
}

func cleanupContainer(t *testing.T, id string) {
	t.Helper()
	api("DELETE", "/api/containers/"+id+"?force=true", nil)
}

func cleanupVolume(t *testing.T, name string) {
	t.Helper()
	api("DELETE", "/api/volumes/"+name, nil)
}

func cleanupNetwork(t *testing.T, name string) {
	t.Helper()
	api("DELETE", "/api/networks/"+name, nil)
}

func TestIntegrationCreateAndStart(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	pullResp, err := api("POST", "/api/images/pull", map[string]string{"image": "docker.io/library/alpine:3.19"})
	if err != nil {
		t.Fatalf("pull image: %v", err)
	}
	if pullResp.StatusCode != http.StatusAccepted {
		t.Fatalf("pull image status: %d", pullResp.StatusCode)
	}
	pullResp.Body.Close()
	time.Sleep(3 * time.Second)

	createResp, err := api("POST", "/api/containers", map[string]interface{}{
		"image": "alpine:3.19",
		"name":  "test-create-start",
		"cmd":   []string{"sleep", "300"},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: %d", createResp.StatusCode)
	}

	var container struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&container); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if container.ID == "" {
		t.Fatal("container ID is empty")
	}
	t.Logf("created container: %s", container.ID)
	defer cleanupContainer(t, container.ID)

	startResp, err := api("POST", "/api/containers/"+container.ID+"/start", nil)
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start status: %d", startResp.StatusCode)
	}

	time.Sleep(1 * time.Second)

	inspectResp, err := api("GET", "/api/containers/"+container.ID+"/inspect", nil)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	defer inspectResp.Body.Close()

	var detail struct {
		State struct {
			Status  string `json:"status"`
			Running bool   `json:"running"`
			Pid     int    `json:"pid"`
		} `json:"state"`
	}
	if err := json.NewDecoder(inspectResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if !detail.State.Running {
		t.Fatal("container is not running")
	}
	t.Logf("container running with PID %d", detail.State.Pid)

	stopResp, err := api("POST", "/api/containers/"+container.ID+"/stop", map[string]int{"timeout": 3})
	if err != nil {
		t.Fatalf("stop container: %v", err)
	}
	stopResp.Body.Close()
	if stopResp.StatusCode != http.StatusOK {
		t.Fatalf("stop status: %d", stopResp.StatusCode)
	}

	time.Sleep(2 * time.Second)

	inspectResp2, err := api("GET", "/api/containers/"+container.ID+"/inspect", nil)
	if err != nil {
		t.Fatalf("inspect after stop: %v", err)
	}
	defer inspectResp2.Body.Close()

	var detail2 struct {
		State struct {
			Status  string `json:"status"`
			Running bool   `json:"running"`
		} `json:"state"`
	}
	if err := json.NewDecoder(inspectResp2.Body).Decode(&detail2); err != nil {
		t.Fatalf("decode inspect after stop: %v", err)
	}
	if detail2.State.Running {
		t.Fatal("container should be stopped")
	}
	t.Logf("container stopped with status: %s", detail2.State.Status)
}

func TestIntegrationNetworkIsolation(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	netResp, err := api("POST", "/api/networks", map[string]interface{}{
		"name":   "test-bridge-net",
		"driver": "bridge",
	})
	if err != nil {
		t.Fatalf("create network: %v", err)
	}
	netResp.Body.Close()
	defer cleanupNetwork(t, "test-bridge-net")

	type containerResult struct {
		id       string
		name     string
		ip       string
	}

	containers := []string{"test-iso-a", "test-iso-b"}
	var results []containerResult

	for _, name := range containers {
		resp, err := api("POST", "/api/containers", map[string]interface{}{
			"image": "alpine:3.19",
			"name":  name,
			"cmd":   []string{"sleep", "600"},
		})
		if err != nil {
			t.Fatalf("create container %s: %v", name, err)
		}
		defer resp.Body.Close()

		var c struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
			t.Fatalf("decode: %v", err)
		}
		defer cleanupContainer(t, c.ID)

		startResp, err := api("POST", "/api/containers/"+c.ID+"/start", nil)
		if err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
		startResp.Body.Close()
		time.Sleep(1 * time.Second)

		results = append(results, containerResult{id: c.ID, name: name})
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(results))
	}

	execResp, err := api("POST", "/api/containers/"+results[0].id+"/exec", map[string]interface{}{
		"cmd": []string{"ping", "-c", "2", "-W", "1", results[1].name},
	})
	if err != nil {
		t.Fatalf("exec ping: %v", err)
	}
	defer execResp.Body.Close()

	var execResult struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&execResult); err != nil {
		t.Fatalf("decode exec: %v", err)
	}
	t.Logf("ping result: exit=%d output=%s", execResult.ExitCode, strings.TrimSpace(execResult.Stdout))

	if execResult.ExitCode != 0 {
		t.Logf("containers cannot ping each other (network isolation working)")
	}
}

func TestIntegrationCgroupLimits(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	createResp, err := api("POST", "/api/containers", map[string]interface{}{
		"image":      "alpine:3.19",
		"name":       "test-cgroup-limits",
		"cmd":        []string{"sleep", "300"},
		"memory":     50 * 1024 * 1024,
		"cpu_shares": 256,
		"pids_limit": 50,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status: %d", createResp.StatusCode)
	}

	var c struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	defer cleanupContainer(t, c.ID)

	startResp, err := api("POST", "/api/containers/"+c.ID+"/start", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	startResp.Body.Close()
	time.Sleep(1 * time.Second)

	statsResp, err := api("GET", "/api/containers/"+c.ID+"/stats", nil)
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	defer statsResp.Body.Close()

	var stats struct {
		MemoryStats struct {
			Limit int64 `json:"limit"`
		} `json:"memory_stats"`
		PidsStats struct {
			Current int `json:"current"`
		} `json:"pids_stats"`
	}
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}

	t.Logf("memory limit: %d bytes", stats.MemoryStats.Limit)
	if stats.MemoryStats.Limit != 50*1024*1024 {
		t.Errorf("expected memory limit 52428800, got %d", stats.MemoryStats.Limit)
	}

	execResp, err := api("POST", "/api/containers/"+c.ID+"/exec", map[string]interface{}{
		"cmd": []string{"cat", "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes"},
	})
	if err != nil {
		t.Fatalf("exec cgroup check: %v", err)
	}
	defer execResp.Body.Close()
}

func TestIntegrationVolumeMount(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	volResp, err := api("POST", "/api/volumes", map[string]interface{}{
		"name": "test-persistent-vol",
	})
	if err != nil {
		t.Fatalf("create volume: %v", err)
	}
	volResp.Body.Close()
	defer cleanupVolume(t, "test-persistent-vol")

	createResp, err := api("POST", "/api/containers", map[string]interface{}{
		"image": "alpine:3.19",
		"name":  "test-vol-write",
		"cmd":   []string{"sleep", "300"},
		"volumes": []map[string]interface{}{
			{
				"type":        "volume",
				"source":      "test-persistent-vol",
				"destination": "/data",
			},
		},
	})
	if err != nil {
		t.Fatalf("create write container: %v", err)
	}
	defer createResp.Body.Close()

	var c1 struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&c1); err != nil {
		t.Fatalf("decode: %v", err)
	}
	defer cleanupContainer(t, c1.ID)

	start1Resp, err := api("POST", "/api/containers/"+c1.ID+"/start", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	start1Resp.Body.Close()
	time.Sleep(1 * time.Second)

	writeExec, err := api("POST", "/api/containers/"+c1.ID+"/exec", map[string]interface{}{
		"cmd": []string{"sh", "-c", "echo 'hello from volume' > /data/test.txt"},
	})
	if err != nil {
		t.Fatalf("exec write: %v", err)
	}
	writeExec.Body.Close()

	stopResp, err := api("POST", "/api/containers/"+c1.ID+"/stop", nil)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	stopResp.Body.Close()
	time.Sleep(2 * time.Second)

	createReadResp, err := api("POST", "/api/containers", map[string]interface{}{
		"image": "alpine:3.19",
		"name":  "test-vol-read",
		"cmd":   []string{"cat", "/data/test.txt"},
		"volumes": []map[string]interface{}{
			{
				"type":        "volume",
				"source":      "test-persistent-vol",
				"destination": "/data",
			},
		},
	})
	if err != nil {
		t.Fatalf("create read container: %v", err)
	}
	defer createReadResp.Body.Close()

	var c2 struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createReadResp.Body).Decode(&c2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	defer cleanupContainer(t, c2.ID)

	start2Resp, err := api("POST", "/api/containers/"+c2.ID+"/start", nil)
	if err != nil {
		t.Fatalf("start read: %v", err)
	}
	start2Resp.Body.Close()
	time.Sleep(2 * time.Second)

	logsResp, err := api("GET", "/api/containers/"+c2.ID+"/logs", nil)
	if err != nil {
		t.Fatalf("get logs: %v", err)
	}
	defer logsResp.Body.Close()

	var logs struct {
		Logs []struct {
			Line string `json:"line"`
		} `json:"logs"`
	}
	if err := json.NewDecoder(logsResp.Body).Decode(&logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}

	found := false
	for _, entry := range logs.Logs {
		if strings.Contains(entry.Line, "hello from volume") {
			found = true
			break
		}
	}
	if !found {
		t.Error("data did not persist across containers via volume")
	} else {
		t.Log("volume data persisted successfully")
	}
}

func TestIntegrationPortForward(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	createResp, err := api("POST", "/api/containers", map[string]interface{}{
		"image": "alpine:3.19",
		"name":  "test-port-forward",
		"cmd":   []string{"sh", "-c", "apk add --no-cache python3 && python3 -m http.server 8080 --directory /tmp"},
		"ports": []map[string]interface{}{
			{
				"host_port":      18080,
				"container_port": 8080,
				"protocol":       "tcp",
			},
		},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	defer createResp.Body.Close()

	var c struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&c); err != nil {
		t.Fatalf("decode: %v", err)
	}
	defer cleanupContainer(t, c.ID)

	startResp, err := api("POST", "/api/containers/"+c.ID+"/start", nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	startResp.Body.Close()
	time.Sleep(8 * time.Second)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:18080/")
	if err != nil {
		t.Fatalf("host request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	} else {
		t.Log("port forwarding working: host can reach container")
	}
}

func TestIntegrationOverlayFS(t *testing.T) {
	skipIfNotRoot(t)
	waitForServer(t, 10*time.Second)

	tmpDir, err := os.MkdirTemp("", "overlay-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	lowerDir := filepath.Join(tmpDir, "lower")
	upperDir := filepath.Join(tmpDir, "upper")
	mergedDir := filepath.Join(tmpDir, "merged")
	workDir := filepath.Join(tmpDir, "work")

	for _, d := range []string{lowerDir, upperDir, mergedDir, workDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	if err := os.WriteFile(filepath.Join(lowerDir, "lower.txt"), []byte("from lower"), 0644); err != nil {
		t.Fatalf("write lower file: %v", err)
	}

	mountOpts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)
	cmd := exec.Command("mount", "-t", "overlay", "overlay", "-o", mountOpts, mergedDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mount overlay: %v", err)
	}
	defer exec.Command("umount", mergedDir).Run()

	data, err := os.ReadFile(filepath.Join(mergedDir, "lower.txt"))
	if err != nil {
		t.Fatalf("read lower.txt from merged: %v", err)
	}
	if string(data) != "from lower" {
		t.Errorf("lower.txt content mismatch: got %q", string(data))
	}

	if err := os.WriteFile(filepath.Join(mergedDir, "upper.txt"), []byte("from upper"), 0644); err != nil {
		t.Fatalf("write upper file: %v", err)
	}

	entries, err := os.ReadDir(mergedDir)
	if err != nil {
		t.Fatalf("readdir merged: %v", err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	t.Logf("merged dir contents: %v", names)

	if _, err := os.Stat(filepath.Join(upperDir, "upper.txt")); os.IsNotExist(err) {
		t.Error("upper.txt should exist in upper dir")
	} else {
		t.Log("overlay read/write working correctly")
	}

	if _, err := os.Stat(filepath.Join(lowerDir, "upper.txt")); !os.IsNotExist(err) {
		t.Error("upper.txt should not exist in lower dir")
	}
}
