//go:build linux

package network

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DNSServer struct {
	listenAddr string
	records    map[string]string
	networks   map[string]*NetworkInfo
	mu         sync.RWMutex
	listener   net.PacketConn
	running    bool
}

type NetworkInfo struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Subnet     string            `json:"subnet"`
	Gateway    string            `json:"gateway"`
	Containers map[string]string `json:"containers"`
}

type DNSRecord struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	TTL  int    `json:"ttl"`
}

func NewDNSServer(listenAddr string) *DNSServer {
	return &DNSServer{
		listenAddr: listenAddr,
		records:    make(map[string]string),
		networks:   make(map[string]*NetworkInfo),
	}
}

func (ds *DNSServer) AddRecord(name, ip string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.records[name] = ip
}

func (ds *DNSServer) RemoveRecord(name string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	delete(ds.records, name)
}

func (ds *DNSServer) Resolve(name string) (string, error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	ip, ok := ds.records[name]
	if !ok {
		return "", fmt.Errorf("dns: record not found: %s", name)
	}
	return ip, nil
}

func (ds *DNSServer) Start() error {
	pc, err := net.ListenPacket("udp", ds.listenAddr)
	if err != nil {
		return fmt.Errorf("dns: failed to listen on %s: %w", ds.listenAddr, err)
	}
	ds.listener = pc
	ds.running = true
	go ds.serve()
	return nil
}

func (ds *DNSServer) Stop() {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.running = false
	if ds.listener != nil {
		ds.listener.Close()
		ds.listener = nil
	}
}

func (ds *DNSServer) serve() {
	buf := make([]byte, 512)
	for {
		ds.mu.RLock()
		if !ds.running {
			ds.mu.RUnlock()
			return
		}
		ds.mu.RUnlock()

		n, addr, err := ds.listener.ReadFrom(buf)
		if err != nil {
			return
		}
		go ds.handleDNSRequest(buf[:n], addr)
	}
}

func (ds *DNSServer) handleDNSRequest(query []byte, addr net.Addr) {
	if len(query) < 12 {
		return
	}
	name := parseDNSName(query)
	if name == "" {
		return
	}

	ds.mu.RLock()
	ip, ok := ds.records[name]
	ds.mu.RUnlock()

	var resp []byte
	if ok {
		resp = buildDNSResponse(query, name, ip)
	} else {
		resp = buildDNSError(query, name)
	}
	ds.listener.WriteTo(resp, addr)
}

func parseDNSName(query []byte) string {
	if len(query) < 12 {
		return ""
	}
	var parts []string
	i := 12
	for i < len(query) {
		length := int(query[i])
		if length == 0 {
			break
		}
		i++
		if i+length > len(query) {
			return ""
		}
		parts = append(parts, string(query[i:i+length]))
		i += length
	}
	return strings.Join(parts, ".")
}

func buildDNSResponse(query []byte, name, ip string) []byte {
	resp := make([]byte, len(query)+16)
	copy(resp, query)
	resp[2] = 0x81
	resp[3] = 0x80
	resp[4] = 0
	resp[5] = 1
	resp[6] = 0
	resp[7] = 1
	resp[8] = 0
	resp[9] = 0
	resp[10] = 0
	resp[11] = 0

	offset := len(query)
	resp[offset] = 0xC0
	resp[offset+1] = 0x0C
	resp[offset+2] = 0
	resp[offset+3] = 1
	resp[offset+4] = 0
	resp[offset+5] = 1
	resp[offset+6] = 0
	resp[offset+7] = 1
	resp[offset+8] = 0
	resp[offset+9] = 0
	resp[offset+10] = 0
	resp[offset+11] = 0x0E
	resp[offset+12] = 0
	resp[offset+13] = 4
	parts := strings.Split(ip, ".")
	for i, p := range parts {
		resp[offset+14+i] = byte(parseInt(p))
	}
	return resp
}

func buildDNSError(query []byte, name string) []byte {
	resp := make([]byte, len(query))
	copy(resp, query)
	resp[2] = 0x81
	resp[3] = 0x83
	return resp
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func (ds *DNSServer) CreateNetwork(name, subnet, gateway string) (*NetworkInfo, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if _, exists := ds.networks[name]; exists {
		return nil, fmt.Errorf("network already exists: %s", name)
	}

	info := &NetworkInfo{
		Name:       name,
		Driver:     "bridge",
		Subnet:     subnet,
		Gateway:    gateway,
		Containers: make(map[string]string),
	}
	ds.networks[name] = info

	ds.records[name+".network"] = gateway
	return info, nil
}

func (ds *DNSServer) DeleteNetwork(name string) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	info, exists := ds.networks[name]
	if !exists {
		return fmt.Errorf("network not found: %s", name)
	}

	for cid := range info.Containers {
		delete(ds.records, cid)
	}
	delete(ds.networks, name)
	delete(ds.records, name+".network")
	return nil
}

func (ds *DNSServer) AttachToNetwork(containerID, networkName, ip string) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	info, exists := ds.networks[networkName]
	if !exists {
		return fmt.Errorf("network not found: %s", networkName)
	}

	if err := validateIPInSubnet(ip, info.Subnet); err != nil {
		return err
	}

	info.Containers[containerID] = ip
	ds.records[containerID] = ip
	return nil
}

func (ds *DNSServer) DetachFromNetwork(containerID, networkName string) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	info, exists := ds.networks[networkName]
	if !exists {
		return fmt.Errorf("network not found: %s", networkName)
	}

	if _, ok := info.Containers[containerID]; !ok {
		return fmt.Errorf("container %s not attached to network %s", containerID, networkName)
	}

	delete(info.Containers, containerID)
	delete(ds.records, containerID)
	return nil
}

func (ds *DNSServer) ListNetworks() []*NetworkInfo {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	networks := make([]*NetworkInfo, 0, len(ds.networks))
	for _, info := range ds.networks {
		networks = append(networks, info)
	}
	return networks
}

func (ds *DNSServer) ResolveService(serviceName string) ([]string, error) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	var ips []string
	for name, ip := range ds.records {
		if strings.HasPrefix(name, serviceName+".") || name == serviceName {
			ips = append(ips, ip)
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("service not found: %s", serviceName)
	}
	return ips, nil
}

func validateIPInSubnet(ip, subnet string) error {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("invalid subnet: %w", err)
	}
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return fmt.Errorf("invalid IP: %s", ip)
	}
	if !ipNet.Contains(parsedIP) {
		return fmt.Errorf("IP %s not in subnet %s", ip, subnet)
	}
	return nil
}

func (ds *DNSServer) SaveRecords(path string) error {
	ds.mu.RLock()
	defer ds.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var data strings.Builder
	for name, ip := range ds.records {
		data.WriteString(fmt.Sprintf("%s %s\n", name, ip))
	}
	return os.WriteFile(path, []byte(data.String()), 0644)
}

func (ds *DNSServer) LoadRecords(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	ds.mu.Lock()
	defer ds.mu.Unlock()

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			ds.records[parts[0]] = parts[1]
		}
	}
	return nil
}

func (ds *DNSServer) GetNetworkByName(name string) (*NetworkInfo, bool) {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	info, ok := ds.networks[name]
	return info, ok
}

func (ds *DNSServer) IsRunning() bool {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return ds.running
}

func (ds *DNSServer) RecordCount() int {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	return len(ds.records)
}

func (ds *DNSServer) CleanupStale(timeout time.Duration) int {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	cleaned := 0
	_ = cleaned
	return 0
}
