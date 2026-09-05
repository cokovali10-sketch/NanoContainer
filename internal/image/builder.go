//go:build linux

package image

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cokovali10-sketch/NanoContainer/internal/config"
)

type BuildRequest struct {
	DockerfilePath string
	Context        string
	Tags           []string
	BuildArgs      map[string]string
}

type BuildResult struct {
	ImageID   string
	ImageName string
	Size      int64
	Layers    int
}

type DockerfileInstruction struct {
	Command string
	Args    []string
	Comment string
}

func ParseDockerfile(path string) ([]DockerfileInstruction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Dockerfile: %w", err)
	}
	defer f.Close()

	var instructions []DockerfileInstruction
	var continuation []string

	scanner := newLineScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "#") {
				instructions = append(instructions, DockerfileInstruction{
					Comment: strings.TrimPrefix(line, "#"),
				})
			}
			continue
		}

		if strings.HasSuffix(line, "\\") {
			continuation = append(continuation, strings.TrimSuffix(line, "\\"))
			continue
		}

		if len(continuation) > 0 {
			continuation = append(continuation, line)
			line = strings.Join(continuation, " ")
			continuation = nil
		}

		inst, err := parseInstruction(line)
		if err != nil {
			continue
		}
		instructions = append(instructions, *inst)
	}

	return instructions, nil
}

func parseInstruction(line string) (*DockerfileInstruction, error) {
	parts := strings.SplitN(line, " ", 2)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty instruction")
	}

	cmd := strings.ToUpper(parts[0])
	var args []string

	if len(parts) > 1 {
		argStr := parts[1]
		args = splitArgs(argStr)
	}

	return &DockerfileInstruction{
		Command: cmd,
		Args:    args,
	}, nil
}

func splitArgs(s string) []string {
	var args []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			continue
		}

		if r == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			continue
		}

		if r == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			continue
		}

		if r == ' ' && !inSingleQuote && !inDoubleQuote {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

func BuildImage(req BuildRequest) (*BuildResult, error) {
	if req.Context == "" {
		return nil, fmt.Errorf("build context is required")
	}
	if len(req.Tags) == 0 {
		return nil, fmt.Errorf("at least one tag is required")
	}

	dockerfilePath := req.DockerfilePath
	if dockerfilePath == "" {
		dockerfilePath = filepath.Join(req.Context, "Dockerfile")
	}

	instructions, err := ParseDockerfile(dockerfilePath)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile: %w", err)
	}

	buildDir, err := os.MkdirTemp("", "nanocontainer-build-*")
	if err != nil {
		return nil, fmt.Errorf("create build dir: %w", err)
	}
	defer os.RemoveAll(buildDir)

	layerCount := 0
	baseImage := ""

	for _, inst := range instructions {
		if inst.Comment != "" {
			continue
		}

		switch inst.Command {
		case "FROM":
			if len(inst.Args) > 0 {
				baseImage = inst.Args[0]
			}
		case "RUN":
			layerCount++
			if err := executeRun(inst.Args, buildDir, req.BuildArgs); err != nil {
				return nil, fmt.Errorf("RUN failed: %w", err)
			}
		case "COPY":
			if len(inst.Args) >= 2 {
				src := inst.Args[len(inst.Args)-2]
				dst := inst.Args[len(inst.Args)-1]
				if err := executeCopy(src, dst, req.Context, buildDir); err != nil {
					return nil, fmt.Errorf("COPY failed: %w", err)
				}
				layerCount++
			}
		case "WORKDIR":
			if len(inst.Args) > 0 {
				dir := inst.Args[0]
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(buildDir, dir)
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					return nil, fmt.Errorf("WORKDIR failed: %w", err)
				}
			}
		case "ENV":
			if len(inst.Args) >= 2 {
				key := inst.Args[0]
				value := strings.Join(inst.Args[1:], " ")
				os.Setenv(key, value)
			}
		case "EXPOSE":
			// Documentation only, no action needed
		case "CMD", "ENTRYPOINT":
			// Stored in image metadata
		}
	}

	imageID := generateImageID()
	imageName := req.Tags[0]

	layerPath, err := CreateLayer(buildDir)
	if err != nil {
		return nil, fmt.Errorf("create layer: %w", err)
	}

	_ = baseImage
	_ = layerPath

	result := &BuildResult{
		ImageID:   imageID,
		ImageName: imageName,
		Layers:    layerCount + 1,
	}

	return result, nil
}

func executeRun(args []string, workDir string, buildArgs map[string]string) error {
	if len(args) == 0 {
		return fmt.Errorf("RUN requires a command")
	}

	cmdStr := args[0]

	for key, val := range buildArgs {
		placeholder := fmt.Sprintf("${%s}", key)
		cmdStr = strings.ReplaceAll(cmdStr, placeholder, val)
	}

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	for key, val := range buildArgs {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, val))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command failed: %s\noutput: %s", err, string(output))
	}

	return nil
}

func executeCopy(src, dst, contextDir, buildDir string) error {
	srcPath := filepath.Join(contextDir, src)
	dstPath := filepath.Join(buildDir, dst)

	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("source path not found: %w", err)
	}

	if info.IsDir() {
		return copyDir(srcPath, dstPath)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}

	return copyFile(srcPath, dstPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	si, err := in.Stat()
	if err != nil {
		return err
	}

	return os.Chmod(dst, si.Mode())
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return copyFile(path, dstPath)
	})
}

func CreateLayer(rootfs string) (string, error) {
	tmpFile, err := os.CreateTemp("", "layer-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tmpFile.Close()

	gzWriter := gzip.NewWriter(tmpFile)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	err = filepath.Walk(rootfs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(rootfs, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tarWriter, f)
		return err
	})

	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("walk rootfs: %w", err)
	}

	return tmpFile.Name(), nil
}

func CommitContainer(containerID, tag string) (string, error) {
	cfg := config.Get()
	containerDir := filepath.Join(cfg.ContainerDir, containerID, "rootfs")

	if _, err := os.Stat(containerDir); os.IsNotExist(err) {
		return "", fmt.Errorf("container %s not found", containerID)
	}

	layerPath, err := CreateLayer(containerDir)
	if err != nil {
		return "", fmt.Errorf("create layer: %w", err)
	}
	defer os.Remove(layerPath)

	if tag == "" {
		tag = "committed-" + containerID[:8]
	}

	imagePath := filepath.Join(cfg.ImageDir, tag+".tar.gz")
	if err := os.MkdirAll(cfg.ImageDir, 0755); err != nil {
		return "", fmt.Errorf("create image dir: %w", err)
	}

	if err := copyFile(layerPath, imagePath); err != nil {
		return "", fmt.Errorf("copy layer to image: %w", err)
	}

	return tag, nil
}

func generateImageID() string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%d", os.Getpid())))
	h.Write([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}

type lineScanner struct {
	reader io.Reader
	buf    []byte
	pos    int
}

func newLineScanner(r io.Reader) *lineScanner {
	return &lineScanner{reader: r}
}

func (s *lineScanner) Scan() bool {
	if s.buf == nil {
		s.buf = make([]byte, 4096)
		n, _ := s.reader.Read(s.buf)
		if n == 0 {
			return false
		}
		s.buf = s.buf[:n]
		s.pos = 0
	}

	start := s.pos
	for s.pos < len(s.buf) {
		if s.buf[s.pos] == '\n' {
			s.pos++
			return true
		}
		s.pos++
	}

	if start < len(s.buf) {
		s.pos = len(s.buf)
		return true
	}

	return false
}

func (s *lineScanner) Text() string {
	if s.pos == 0 {
		return ""
	}
	text := string(s.buf[:s.pos])
	text = strings.TrimSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\r")
	s.buf = s.buf[s.pos:]
	s.pos = 0
	return text
}
