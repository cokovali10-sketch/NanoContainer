//go:build linux

package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	StorageDir string
	Addr       string
	mu         sync.RWMutex
}

type ImageManifest struct {
	Name      string      `json:"name"`
	Tag       string      `json:"tag"`
	Digest    string      `json:"digest"`
	Size      int64       `json:"size"`
	CreatedAt time.Time   `json:"created_at"`
	Layers    []LayerInfo `json:"layers"`
}

type LayerInfo struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func NewRegistry(storageDir, addr string) *Registry {
	return &Registry{
		StorageDir: storageDir,
		Addr:       addr,
	}
}

func (r *Registry) PushImage(name, tag, tarballPath string) (*ImageManifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.MkdirAll(r.StorageDir, 0755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}

	digest, size, err := GetDigest(tarballPath)
	if err != nil {
		return nil, fmt.Errorf("calculate digest: %w", err)
	}

	imgDir := filepath.Join(r.StorageDir, name)
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		return nil, fmt.Errorf("create image dir: %w", err)
	}

	blobPath := filepath.Join(imgDir, digest)
	if err := copyFile(tarballPath, blobPath); err != nil {
		return nil, fmt.Errorf("copy tarball: %w", err)
	}

	manifest := &ImageManifest{
		Name:      name,
		Tag:       tag,
		Digest:    digest,
		Size:      size,
		CreatedAt: time.Now(),
		Layers: []LayerInfo{
			{Digest: digest, Size: size},
		},
	}

	manifestPath := filepath.Join(imgDir, tag+".json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}

	tmpPath := manifestPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	if err := os.Rename(tmpPath, manifestPath); err != nil {
		return nil, fmt.Errorf("save manifest: %w", err)
	}

	return manifest, nil
}

func (r *Registry) PullImage(name, tag, destPath string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	manifest, err := r.getImageManifest(name, tag)
	if err != nil {
		return err
	}

	blobPath := filepath.Join(r.StorageDir, name, manifest.Digest)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		return fmt.Errorf("blob %s not found", manifest.Digest)
	}

	return copyFile(blobPath, destPath)
}

func (r *Registry) ListImages() ([]*ImageManifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries, err := os.ReadDir(r.StorageDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read storage dir: %w", err)
	}

	var images []*ImageManifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		imgDir := filepath.Join(r.StorageDir, name)
		tagFiles, err := os.ReadDir(imgDir)
		if err != nil {
			continue
		}

		for _, tf := range tagFiles {
			if tf.IsDir() || !strings.HasSuffix(tf.Name(), ".json") {
				continue
			}

			tag := strings.TrimSuffix(tf.Name(), ".json")
			manifest, err := r.getImageManifest(name, tag)
			if err != nil {
				continue
			}
			images = append(images, manifest)
		}
	}

	return images, nil
}

func (r *Registry) GetImage(name, tag string) (*ImageManifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.getImageManifest(name, tag)
}

func (r *Registry) getImageManifest(name, tag string) (*ImageManifest, error) {
	manifestPath := filepath.Join(r.StorageDir, name, tag+".json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("image %s:%s not found", name, tag)
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	manifest := &ImageManifest{}
	if err := json.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	return manifest, nil
}

func (r *Registry) DeleteImage(name, tag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	manifest, err := r.getImageManifest(name, tag)
	if err != nil {
		return err
	}

	manifestPath := filepath.Join(r.StorageDir, name, tag+".json")
	if err := os.Remove(manifestPath); err != nil {
		return fmt.Errorf("delete manifest: %w", err)
	}

	blobPath := filepath.Join(r.StorageDir, name, manifest.Digest)
	if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete blob: %w", err)
	}

	imgDir := filepath.Join(r.StorageDir, name)
	entries, err := os.ReadDir(imgDir)
	if err == nil && len(entries) == 0 {
		os.Remove(imgDir)
	}

	return nil
}

func (r *Registry) TagImage(name, tag, newTag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	manifest, err := r.getImageManifest(name, tag)
	if err != nil {
		return err
	}

	newManifest := *manifest
	newManifest.Tag = newTag
	newManifest.CreatedAt = time.Now()

	manifestPath := filepath.Join(r.StorageDir, name, newTag+".json")
	data, err := json.MarshalIndent(newManifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(r.StorageDir, name), 0755); err != nil {
		return fmt.Errorf("create image dir: %w", err)
	}

	tmpPath := manifestPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return os.Rename(tmpPath, manifestPath)
}

func GetDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("read file: %w", err)
	}

	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	return digest, size, nil
}

func (r *Registry) StartHTTPServer(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/v2/")
		parts := strings.Split(path, "/")

		if len(parts) < 2 {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}

		name := parts[0]

		switch {
		case parts[1] == "manifests" && len(parts) == 3:
			tag := parts[2]
			r.handleManifest(w, req, name, tag)
		case parts[1] == "blobs" && len(parts) == 3:
			digest := parts[2]
			r.handleBlob(w, req, name, digest)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return server.ListenAndServe()
}

func (r *Registry) handleManifest(w http.ResponseWriter, req *http.Request, name, tag string) {
	switch req.Method {
	case http.MethodGet:
		manifest, err := r.GetImage(name, tag)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		data, err := json.Marshal(manifest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Docker-Content-Digest", manifest.Digest)
		w.Write(data)

	case http.MethodPut:
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var manifest ImageManifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		imgDir := filepath.Join(r.StorageDir, name)
		if err := os.MkdirAll(imgDir, 0755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		manifestPath := filepath.Join(imgDir, tag+".json")
		if err := os.WriteFile(manifestPath, body, 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)

	case http.MethodDelete:
		if err := r.DeleteImage(name, tag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (r *Registry) handleBlob(w http.ResponseWriter, req *http.Request, name, digest string) {
	if req.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	blobPath := filepath.Join(r.StorageDir, name, digest)
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		http.Error(w, "blob not found", http.StatusNotFound)
		return
	}

	f, err := os.Open(blobPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	io.Copy(w, f)
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
