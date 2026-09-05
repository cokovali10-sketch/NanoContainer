//go:build linux

package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Template struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Ports       []PortMapping     `json:"ports,omitempty"`
	Volumes     []VolumeMount     `json:"volumes,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Resources   ResourceLimits    `json:"resources"`
	Files       map[string]string `json:"files,omitempty"`
}

type PortMapping struct {
	Host      int `json:"host"`
	Container int `json:"container"`
}

type VolumeMount struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type ResourceLimits struct {
	CPU    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
}

var BuiltInTemplates = map[string]*Template{
	"nginx":        NginxTemplate,
	"node":         NodeTemplate,
	"python":       PythonTemplate,
	"redis":        RedisTemplate,
	"postgres":     PostgresTemplate,
	"mysql":        MySQLTemplate,
	"mongo":        MongoTemplate,
	"golang":       GoTemplate,
	"rust":         RustTemplate,
	"java":         JavaTemplate,
	"php":          PHPTemplate,
	"apache":       ApacheTemplate,
	"mongo-express": MongoExpressTemplate,
	"adminer":      AdminerTemplate,
	"portainer":    PortainerTemplate,
}

var NginxTemplate = &Template{
	Name:        "nginx",
	Description: "Nginx web server",
	Image:       "alpine",
	Command:     []string{"nginx", "-g", "daemon off;"},
	Ports:       []PortMapping{{Host: 80, Container: 80}, {Host: 443, Container: 443}},
	Volumes:     []VolumeMount{{Source: "nginx-html", Target: "/usr/share/nginx/html"}},
	Resources:   ResourceLimits{CPU: 50000, Memory: 268435456},
	Files: map[string]string{
		"nginx.conf": `worker_processes auto;
events {
    worker_connections 1024;
}
http {
    server {
        listen 80;
        server_name localhost;
        root /usr/share/nginx/html;
        index index.html;
        location / {
            try_files $uri $uri/ =404;
        }
    }
}`,
		"index.html": `<!DOCTYPE html>
<html><head><title>NanoContainer</title></head>
<body><h1>Hello from NanoContainer + Nginx</h1></body>
</html>`,
	},
}

var NodeTemplate = &Template{
	Name:        "node",
	Description: "Node.js application server",
	Image:       "alpine",
	Command:     []string{"node", "-e", "require('http').createServer((req,res)=>{res.end('Hello from Node')}).listen(3000)"},
	Ports:       []PortMapping{{Host: 3000, Container: 3000}},
	Environment: map[string]string{"NODE_ENV": "production"},
	Resources:   ResourceLimits{CPU: 100000, Memory: 536870912},
}

var PythonTemplate = &Template{
	Name:        "python",
	Description: "Python application server",
	Image:       "alpine",
	Command:     []string{"python3", "-m", "http.server", "8080"},
	Ports:       []PortMapping{{Host: 8080, Container: 8080}},
	Environment: map[string]string{"PYTHONUNBUFFERED": "1"},
	Resources:   ResourceLimits{CPU: 100000, Memory: 268435456},
}

var RedisTemplate = &Template{
	Name:        "redis",
	Description: "Redis in-memory data store",
	Image:       "alpine",
	Command:     []string{"redis-server", "--appendonly", "yes", "--maxmemory", "128mb"},
	Ports:       []PortMapping{{Host: 6379, Container: 6379}},
	Volumes:     []VolumeMount{{Source: "redis-data", Target: "/data"}},
	Resources:   ResourceLimits{CPU: 50000, Memory: 134217728},
}

var PostgresTemplate = &Template{
	Name:        "postgres",
	Description: "PostgreSQL relational database",
	Image:       "alpine",
	Command:     []string{"postgres", "-D", "/var/lib/postgresql/data"},
	Ports:       []PortMapping{{Host: 5432, Container: 5432}},
	Environment: map[string]string{
		"POSTGRES_USER":     "postgres",
		"POSTGRES_PASSWORD": "postgres",
		"POSTGRES_DB":       "app",
	},
	Volumes:   []VolumeMount{{Source: "postgres-data", Target: "/var/lib/postgresql/data"}},
	Resources: ResourceLimits{CPU: 200000, Memory: 536870912},
}

var MySQLTemplate = &Template{
	Name:        "mysql",
	Description: "MySQL relational database",
	Image:       "alpine",
	Command:     []string{"mysqld", "--user=mysql"},
	Ports:       []PortMapping{{Host: 3306, Container: 3306}},
	Environment: map[string]string{
		"MYSQL_ROOT_PASSWORD": "root",
		"MYSQL_DATABASE":      "app",
		"MYSQL_USER":          "app",
		"MYSQL_PASSWORD":      "app",
	},
	Volumes:   []VolumeMount{{Source: "mysql-data", Target: "/var/lib/mysql"}},
	Resources: ResourceLimits{CPU: 200000, Memory: 536870912},
}

var MongoTemplate = &Template{
	Name:        "mongo",
	Description: "MongoDB document database",
	Image:       "alpine",
	Command:     []string{"mongod", "--dbpath", "/data/db"},
	Ports:       []PortMapping{{Host: 27017, Container: 27017}},
	Environment: map[string]string{"MONGO_INITDB_ROOT_USERNAME": "admin", "MONGO_INITDB_ROOT_PASSWORD": "admin"},
	Volumes:     []VolumeMount{{Source: "mongo-data", Target: "/data/db"}},
	Resources:   ResourceLimits{CPU: 200000, Memory: 536870912},
}

var GoTemplate = &Template{
	Name:        "golang",
	Description: "Go build environment",
	Image:       "alpine",
	Command:     []string{"go", "version"},
	Environment: map[string]string{
		"GOPATH":     "/go",
		"GOROOT":     "/usr/local/go",
		"PATH":       "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	},
	Resources: ResourceLimits{CPU: 200000, Memory: 1073741824},
}

var RustTemplate = &Template{
	Name:        "rust",
	Description: "Rust build environment",
	Image:       "alpine",
	Command:     []string{"rustc", "--version"},
	Environment: map[string]string{
		"CARGO_HOME": "/cargo",
		"RUSTUP_HOME": "/rustup",
		"PATH":        "/cargo/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	},
	Volumes:   []VolumeMount{{Source: "cargo-cache", Target: "/cargo"}},
	Resources: ResourceLimits{CPU: 200000, Memory: 1073741824},
}

var JavaTemplate = &Template{
	Name:        "java",
	Description: "Java runtime environment",
	Image:       "alpine",
	Command:     []string{"java", "-version"},
	Environment: map[string]string{
		"JAVA_HOME": "/usr/lib/jvm/default-jvm",
		"PATH":      "/usr/lib/jvm/default-jvm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	},
	Resources: ResourceLimits{CPU: 200000, Memory: 1073741824},
}

var PHPTemplate = &Template{
	Name:        "php",
	Description: "PHP application server",
	Image:       "alpine",
	Command:     []string{"php", "-S", "0.0.0.0:8080"},
	Ports:       []PortMapping{{Host: 8080, Container: 8080}},
	Environment: map[string]string{"PHP_INI_SCAN_DIR": "/usr/local/etc/php/conf.d"},
	Resources:   ResourceLimits{CPU: 100000, Memory: 268435456},
}

var ApacheTemplate = &Template{
	Name:        "apache",
	Description: "Apache HTTP server",
	Image:       "alpine",
	Command:     []string{"httpd", "-D", "FOREGROUND"},
	Ports:       []PortMapping{{Host: 80, Container: 80}},
	Volumes:     []VolumeMount{{Source: "apache-html", Target: "/usr/local/apache2/htdocs"}},
	Resources:   ResourceLimits{CPU: 50000, Memory: 268435456},
}

var MongoExpressTemplate = &Template{
	Name:        "mongo-express",
	Description: "MongoDB web admin interface",
	Image:       "alpine",
	Command:     []string{"mongo-express"},
	Ports:       []PortMapping{{Host: 8081, Container: 8081}},
	Environment: map[string]string{
		"ME_CONFIG_MONGODB_SERVER":  "localhost",
		"ME_CONFIG_MONGODB_PORT":    "27017",
		"ME_CONFIG_MONGODB_ADMINUSERNAME": "admin",
		"ME_CONFIG_MONGODB_ADMINPASSWORD": "admin",
	},
	Resources: ResourceLimits{CPU: 50000, Memory: 268435456},
}

var AdminerTemplate = &Template{
	Name:        "adminer",
	Description: "Database management tool",
	Image:       "alpine",
	Command:     []string{"php", "-S", "0.0.0.0:8080"},
	Ports:       []PortMapping{{Host: 8080, Container: 8080}},
	Environment: map[string]string{"ADMINER_DEFAULT_SERVER": "localhost"},
	Resources:   ResourceLimits{CPU: 50000, Memory: 134217728},
}

var PortainerTemplate = &Template{
	Name:        "portainer",
	Description: "Container management UI",
	Image:       "alpine",
	Command:     []string{"portainer"},
	Ports:       []PortMapping{{Host: 9000, Container: 9000}},
	Volumes:     []VolumeMount{{Source: "portainer-data", Target: "/data"}},
	Resources:   ResourceLimits{CPU: 100000, Memory: 268435456},
}

func GetTemplate(name string) (*Template, bool) {
	tmpl, ok := BuiltInTemplates[name]
	return tmpl, ok
}

func ListTemplates() []string {
	names := make([]string, 0, len(BuiltInTemplates))
	for name := range BuiltInTemplates {
		names = append(names, name)
	}
	return names
}

func DeployTemplate(name, containerName string) (string, error) {
	tmpl, ok := BuiltInTemplates[name]
	if !ok {
		return "", fmt.Errorf("template %q not found", name)
	}

	if containerName == "" {
		containerName = tmpl.Name + "-container"
	}

	type ContainerRequest struct {
		Name    string            `json:"name"`
		Image   string            `json:"image"`
		Command []string          `json:"command,omitempty"`
		Ports   []PortMapping     `json:"ports,omitempty"`
		Env     map[string]string `json:"environment,omitempty"`
		CPU     int64             `json:"cpu_quota"`
		Memory  int64             `json:"mem_limit"`
	}

	req := ContainerRequest{
		Name:    containerName,
		Image:   tmpl.Image,
		Command: tmpl.Command,
		Ports:   tmpl.Ports,
		Env:     tmpl.Environment,
		CPU:     tmpl.Resources.CPU,
		Memory:  tmpl.Resources.Memory,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	_ = data

	return containerName, nil
}

func GenerateCompose(templateNames []string) (string, error) {
	type ComposeVolume struct {
		Name   string `json:"name"`
		Target string `json:"target"`
	}

	type ComposeService struct {
		Image       string            `json:"image"`
		Command     []string          `json:"command,omitempty"`
		Ports       []string          `json:"ports,omitempty"`
		Volumes     []string          `json:"volumes,omitempty"`
		Environment map[string]string `json:"environment,omitempty"`
		Restart     string            `json:"restart,omitempty"`
	}

	type ComposeFile struct {
		Version  string                   `json:"version"`
		Services map[string]ComposeService `json:"services"`
		Volumes  map[string]struct{}       `json:"volumes,omitempty"`
	}

	compose := ComposeFile{
		Version:  "3.8",
		Services: make(map[string]ComposeService),
		Volumes:  make(map[string]struct{}),
	}

	usedPorts := make(map[int]bool)
	usedNames := make(map[string]int)

	for _, name := range templateNames {
		tmpl, ok := BuiltInTemplates[name]
		if !ok {
			return "", fmt.Errorf("template %q not found", name)
		}

		svcName := name
		if count := usedNames[name]; count > 0 {
			svcName = fmt.Sprintf("%s-%d", name, count+1)
		}
		usedNames[name]++

		svc := ComposeService{
			Image:       tmpl.Image,
			Command:     tmpl.Command,
			Environment: tmpl.Environment,
			Restart:     "unless-stopped",
		}

		for _, port := range tmpl.Ports {
			hostPort := port.Host
			for usedPorts[hostPort] {
				hostPort++
			}
			usedPorts[hostPort] = true
			svc.Ports = append(svc.Ports, fmt.Sprintf("%d:%d", hostPort, port.Container))
		}

		for _, vol := range tmpl.Volumes {
			volName := fmt.Sprintf("nanocontainer-%s-%s", svcName, vol.Source)
			svc.Volumes = append(svc.Volumes, fmt.Sprintf("%s:%s", volName, vol.Target))
			compose.Volumes[volName] = struct{}{}
		}

		compose.Services[svcName] = svc
	}

	data, err := json.MarshalIndent(compose, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal compose: %w", err)
	}

	return string(data), nil
}

func LoadTemplate(path string) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}

	tmpl := &Template{}
	if err := json.Unmarshal(data, tmpl); err != nil {
		return nil, fmt.Errorf("unmarshal template: %w", err)
	}

	return tmpl, nil
}

func SaveTemplate(tmpl *Template, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal template: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write template: %w", err)
	}
	return os.Rename(tmp, path)
}

func ListTemplateNames() []string {
	names := make([]string, 0, len(BuiltInTemplates))
	for name := range BuiltInTemplates {
		names = append(names, name)
	}
	return names
}

func FormatTemplateList() string {
	var b strings.Builder

	b.WriteString("Available Templates:\n")
	b.WriteString("═══════════════════\n\n")

	for name, tmpl := range BuiltInTemplates {
		b.WriteString(fmt.Sprintf("  %-15s  %s\n", name, tmpl.Description))
	}

	b.WriteString(fmt.Sprintf("\nTotal: %d templates\n", len(BuiltInTemplates)))
	return b.String()
}

func FormatTemplateDetail(tmpl *Template) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Template: %s\n", tmpl.Name))
	b.WriteString(fmt.Sprintf("Description: %s\n", tmpl.Description))
	b.WriteString(fmt.Sprintf("Image: %s\n", tmpl.Image))

	if len(tmpl.Command) > 0 {
		b.WriteString(fmt.Sprintf("Command: %s\n", strings.Join(tmpl.Command, " ")))
	}

	if len(tmpl.Ports) > 0 {
		b.WriteString("Ports:\n")
		for _, p := range tmpl.Ports {
			b.WriteString(fmt.Sprintf("  %d -> %d\n", p.Host, p.Container))
		}
	}

	if len(tmpl.Volumes) > 0 {
		b.WriteString("Volumes:\n")
		for _, v := range tmpl.Volumes {
			b.WriteString(fmt.Sprintf("  %s -> %s\n", v.Source, v.Target))
		}
	}

	if len(tmpl.Environment) > 0 {
		b.WriteString("Environment:\n")
		for k, v := range tmpl.Environment {
			b.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
		}
	}

	if len(tmpl.Files) > 0 {
		b.WriteString("Files:\n")
		for name := range tmpl.Files {
			b.WriteString(fmt.Sprintf("  %s\n", name))
		}
	}

	return b.String()
}
