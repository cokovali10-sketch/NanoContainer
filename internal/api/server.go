//go:build linux

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
	"github.com/cokovali10-sketch/NanoContainer/internal/container"
	"github.com/cokovali10-sketch/NanoContainer/internal/rootfs"
	"github.com/cokovali10-sketch/NanoContainer/internal/store"
)

type Server struct {
	cfg  *config.Config
	addr string
	mux  *http.ServeMux
}

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

func NewServer(cfg *config.Config, addr string) *Server {
	s := &Server{
		cfg:  cfg,
		addr: addr,
		mux:  http.NewServeMux(),
	}
	s.SetupRoutes()
	return s
}

func (s *Server) SetupRoutes() {
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/containers", s.routeContainers)
	s.mux.HandleFunc("/api/containers/", s.routeContainerByID)
	s.mux.HandleFunc("/api/images", s.routeImages)
	s.mux.HandleFunc("/api/images/pull", s.handlePullImage)
	s.mux.HandleFunc("/api/images/build", s.handleBuildImage)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	s.mux.HandleFunc("/api/metrics", s.handleMetrics)
	s.mux.HandleFunc("/api/compose", s.routeCompose)
	s.mux.HandleFunc("/api/compose/", s.routeComposeByName)
	s.mux.HandleFunc("/", s.ServeDashboard)
}

func (s *Server) routeContainers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListContainers(w, r)
	case http.MethodPost:
		s.handleCreateContainer(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeContainerByID(w http.ResponseWriter, r *http.Request) {
	id := getParam(r, "/api/containers/")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "container ID required")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/containers/"+id)
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		s.handleGetContainer(w, r, id)
	case path == "stop" && r.Method == http.MethodPost:
		s.handleStopContainer(w, r, id)
	case path == "start" && r.Method == http.MethodPost:
		s.handleStartContainer(w, r, id)
	case path == "" && r.Method == http.MethodDelete:
		s.handleRemoveContainer(w, r, id)
	case path == "logs" && r.Method == http.MethodGet:
		s.handleGetLogs(w, r, id)
	default:
		jsonError(w, http.StatusNotFound, "endpoint not found")
	}
}

func (s *Server) routeImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.handleListImages(w, r)
}

func (s *Server) routeCompose(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleComposeList(w, r)
	case http.MethodPost:
		s.handleComposeDeploy(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) routeComposeByName(w http.ResponseWriter, r *http.Request) {
	name := getParam(r, "/api/compose/")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "stack name required")
		return
	}
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.handleComposeRemove(w, r, name)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]string{
			"status":  "healthy",
			"version": "1.0.0",
			"time":    time.Now().UTC().Format(time.RFC3339),
		},
	})
}

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	includeAll := r.URL.Query().Get("all") == "true"
	stateStore := store.New(s.cfg.ContainerDir)
	states, err := stateStore.List(includeAll)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("list containers: %v", err))
		return
	}
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    states,
	})
}

func (s *Server) handleCreateContainer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string            `json:"name"`
		Image   string            `json:"image"`
		Command []string          `json:"command"`
		CPU     int64             `json:"cpu_quota"`
		Memory  int64             `json:"mem_limit"`
		Labels  map[string]string `json:"labels"`
	}
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Image == "" {
		jsonError(w, http.StatusBadRequest, "image is required")
		return
	}
	if len(req.Command) == 0 {
		req.Command = []string{"/bin/sh"}
	}

	cont, err := container.Create(req.Name, req.Image, req.Command, req.CPU, req.Memory, *s.cfg)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("create container: %v", err))
		return
	}

	detach := r.URL.Query().Get("detach") == "true"
	if err := container.Start(cont, *s.cfg, !detach); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("start container: %v", err))
		return
	}

	stateStore := store.New(s.cfg.ContainerDir)
	state, _ := stateStore.Load(cont.ID)

	jsonRespond(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    state,
	})
}

func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request, id string) {
	id = normalizeID(id)
	stateStore := store.New(s.cfg.ContainerDir)
	state, err := stateStore.Load(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("container %s not found", id))
		return
	}
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    state,
	})
}

func (s *Server) handleStopContainer(w http.ResponseWriter, r *http.Request, id string) {
	id = normalizeID(id)
	if err := container.Stop(id, *s.cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("stop container: %v", err))
		return
	}
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]string{"id": id, "status": "stopped"},
	})
}

func (s *Server) handleStartContainer(w http.ResponseWriter, r *http.Request, id string) {
	id = normalizeID(id)
	stateStore := store.New(s.cfg.ContainerDir)
	state, err := stateStore.Load(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("container %s not found", id))
		return
	}
	if state.Status == store.StatusRunning {
		jsonError(w, http.StatusBadRequest, "container is already running")
		return
	}

	cont := &container.Container{
		ID:         state.ID,
		Name:       state.Name,
		Image:      state.Image,
		Command:    state.Command,
		RootfsPath: state.RootfsPath,
		Hostname:   state.Hostname,
		CPUQuota:   state.CPUQuota,
		MemLimit:   state.MemLimit,
	}

	detach := r.URL.Query().Get("detach") == "true"
	if err := container.Start(cont, *s.cfg, !detach); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("start container: %v", err))
		return
	}

	updated, _ := stateStore.Load(id)
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    updated,
	})
}

func (s *Server) handleRemoveContainer(w http.ResponseWriter, r *http.Request, id string) {
	id = normalizeID(id)
	stateStore := store.New(s.cfg.ContainerDir)
	state, err := stateStore.Load(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("container %s not found", id))
		return
	}
	if state.Status == store.StatusRunning {
		if err := container.Stop(id, *s.cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("stop before remove: %v", err))
			return
		}
	}

	containerDir := filepath.Join(s.cfg.ContainerDir, id)
	if err := os.RemoveAll(containerDir); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("remove container: %v", err))
		return
	}
	if err := stateStore.Delete(id); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("delete state: %v", err))
		return
	}

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]string{"id": id, "status": "removed"},
	})
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request, id string) {
	id = normalizeID(id)
	logDir := filepath.Join(s.cfg.ContainerDir, id)
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	logs := map[string]string{}
	if data, err := os.ReadFile(stdoutPath); err == nil {
		logs["stdout"] = string(data)
	}
	if data, err := os.ReadFile(stderrPath); err == nil {
		logs["stderr"] = string(data)
	}

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    logs,
	})
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	rfs := rootfs.New(s.cfg.ImageDir, s.cfg.ContainerDir)
	images, err := rfs.ListImages()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("list images: %v", err))
		return
	}
	type imageInfo struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Exists bool   `exists:"exists"`
	}
	var result []imageInfo
	for _, img := range images {
		result = append(result, imageInfo{
			Name:   img,
			Path:   filepath.Join(s.cfg.ImageDir, img+".tar.gz"),
			Exists: true,
		})
	}
	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    result,
	})
}

func (s *Server) handlePullImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Name == "" || req.URL == "" {
		jsonError(w, http.StatusBadRequest, "name and url are required")
		return
	}

	rfs := rootfs.New(s.cfg.ImageDir, s.cfg.ContainerDir)
	if err := rfs.PullImage(req.Name, req.URL); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("pull image: %v", err))
		return
	}

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]string{
			"name": req.Name,
			"status": "pulled",
		},
	})
}

func (s *Server) handleBuildImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dockerfile string            `json:"dockerfile"`
		Context    string            `json:"context"`
		Tags       []string          `json:"tags"`
		BuildArgs  map[string]string `json:"build_args"`
	}
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Context == "" {
		jsonError(w, http.StatusBadRequest, "context path is required")
		return
	}
	if req.Dockerfile == "" {
		req.Dockerfile = "Dockerfile"
	}
	if len(req.Tags) == 0 {
		req.Tags = []string{"built-image"}
	}

	dockerfilePath := filepath.Join(req.Context, req.Dockerfile)
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("Dockerfile not found at %s", dockerfilePath))
		return
	}

	buildID := container.GenerateID()
	imageName := req.Tags[0]
	imageDir := filepath.Join(s.cfg.ImageDir, imageName)
	buildDir := filepath.Join(s.cfg.ContainerDir, "builds", buildID)

	if err := os.MkdirAll(buildDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("create build dir: %v", err))
		return
	}
	defer os.RemoveAll(buildDir)

	_ = imageDir

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data: map[string]string{
			"build_id": buildID,
			"image":    imageName,
			"status":   "building",
		},
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := GetSystemStats()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("get stats: %v", err))
		return
	}

	stateStore := store.New(s.cfg.ContainerDir)
	states, _ := stateStore.List(true)
	running := 0
	stopped := 0
	for _, st := range states {
		if st.Status == store.StatusRunning {
			running++
		} else {
			stopped++
		}
	}

	stats.ContainersTotal = len(states)
	stats.ContainersRunning = running
	stats.ContainersStopped = stopped

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    stats,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	stats, err := GetSystemStats()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("get metrics: %v", err))
		return
	}

	stateStore := store.New(s.cfg.ContainerDir)
	states, _ := stateStore.List(true)
	running := 0
	for _, st := range states {
		if st.Status == store.StatusRunning {
			running++
		}
	}

	stats.ContainersTotal = len(states)
	stats.ContainersRunning = running

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprint(w, FormatPrometheusMetrics(stats, running, len(states)))
}

func (s *Server) handleComposeList(w http.ResponseWriter, r *http.Request) {
	stacksDir := filepath.Join(s.cfg.RootDir, "stacks")
	entries, err := os.ReadDir(stacksDir)
	if err != nil {
		jsonRespond(w, http.StatusOK, APIResponse{
			Success: true,
			Data:    []interface{}{},
		})
		return
	}

	var stacks []map[string]interface{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		stackFile := filepath.Join(stacksDir, e.Name(), "stack.json")
		data, err := os.ReadFile(stackFile)
		if err != nil {
			continue
		}
		var stack map[string]interface{}
		if err := json.Unmarshal(data, &stack); err != nil {
			continue
		}
		stacks = append(stacks, stack)
	}

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    stacks,
	})
}

func (s *Server) handleComposeDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string                   `json:"name"`
		Services []map[string]interface{} `json:"services"`
	}
	if err := parseJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "stack name is required")
		return
	}
	if len(req.Services) == 0 {
		jsonError(w, http.StatusBadRequest, "at least one service is required")
		return
	}

	stacksDir := filepath.Join(s.cfg.RootDir, "stacks", req.Name)
	if err := os.MkdirAll(stacksDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("create stack dir: %v", err))
		return
	}

	stackData := map[string]interface{}{
		"name":     req.Name,
		"services": req.Services,
		"status":   "deploying",
	}
	data, _ := json.MarshalIndent(stackData, "", "  ")
	if err := os.WriteFile(filepath.Join(stacksDir, "stack.json"), data, 0644); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("write stack file: %v", err))
		return
	}

	for _, svc := range req.Services {
		svcName, _ := svc["name"].(string)
		image, _ := svc["image"].(string)
		if svcName == "" || image == "" {
			continue
		}

		cmd := []string{"/bin/sh"}
		if cmdRaw, ok := svc["command"].([]interface{}); ok {
			cmd = make([]string, 0, len(cmdRaw))
			for _, c := range cmdRaw {
				if s, ok := c.(string); ok {
					cmd = append(cmd, s)
				}
			}
		}

		var cpuQuota, memLimit int64
		if v, ok := svc["cpu_quota"].(float64); ok {
			cpuQuota = int64(v)
		}
		if v, ok := svc["mem_limit"].(float64); ok {
			memLimit = int64(v)
		}

		cont, err := container.Create(req.Name+"-"+svcName, image, cmd, cpuQuota, memLimit, *s.cfg)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("create service %s: %v", svcName, err))
			return
		}
		if err := container.Start(cont, *s.cfg, false); err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("start service %s: %v", svcName, err))
			return
		}
	}

	stackData["status"] = "running"
	data, _ = json.MarshalIndent(stackData, "", "  ")
	_ = os.WriteFile(filepath.Join(stacksDir, "stack.json"), data, 0644)

	jsonRespond(w, http.StatusCreated, APIResponse{
		Success: true,
		Data:    stackData,
	})
}

func (s *Server) handleComposeRemove(w http.ResponseWriter, r *http.Request, name string) {
	stackDir := filepath.Join(s.cfg.RootDir, "stacks", name)
	if _, err := os.Stat(stackDir); os.IsNotExist(err) {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("stack %s not found", name))
		return
	}

	stateStore := store.New(s.cfg.ContainerDir)
	states, _ := stateStore.List(true)
	for _, st := range states {
		if strings.HasPrefix(st.Name, name+"-") && st.Status == store.StatusRunning {
			_ = container.Stop(st.ID, *s.cfg)
		}
	}

	if err := os.RemoveAll(stackDir); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("remove stack: %v", err))
		return
	}

	jsonRespond(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    map[string]string{"name": name, "status": "removed"},
	})
}

func (s *Server) Start() error {
	handler := RecoveryMiddleware(
		RequestIDMiddleware(
			LoggingMiddleware(
				CORSMiddleware(s.mux),
			),
		),
	)

	server := &http.Server{
		Addr:         s.addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Printf("API server listening on %s\n", s.addr)
	return server.ListenAndServe()
}

func jsonRespond(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonRespond(w, status, APIResponse{
		Success: false,
		Error:   msg,
	})
}

func parseJSON(r *http.Request, v interface{}) error {
	decoder := json.NewDecoder(r.Body)
	return decoder.Decode(v)
}

func getParam(r *http.Request, prefix string) string {
	return strings.TrimPrefix(r.URL.Path, prefix)
}

func normalizeID(id string) string {
	if len(id) > 12 {
		return id
	}
	return id
}

func parseIntParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}
