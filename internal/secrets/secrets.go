//go:build linux

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Secret struct {
	Name      string            `json:"name"`
	Value     string            `json:"value"`
	Type      string            `json:"type"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type SecretsManager struct {
	secretsDir string
	cipher     cipher.AEAD
}

func NewSecretsManager(secretsDir, encryptionKey string) (*SecretsManager, error) {
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("secrets: failed to create directory: %w", err)
	}

	key := deriveKey(encryptionKey)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: failed to create GCM: %w", err)
	}

	return &SecretsManager{
		secretsDir: secretsDir,
		cipher:     gcm,
	}, nil
}

func (sm *SecretsManager) CreateSecret(name, value, secretType string, labels map[string]string) error {
	path := sm.secretPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("secrets: already exists: %s", name)
	}

	encrypted, err := encrypt(value, sm.cipher)
	if err != nil {
		return fmt.Errorf("secrets: encryption failed: %w", err)
	}

	secret := &Secret{
		Name:      name,
		Value:     encrypted,
		Type:      secretType,
		Labels:    labels,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	data, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal failed: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("secrets: write failed: %w", err)
	}

	return nil
}

func (sm *SecretsManager) GetSecret(name string) (*Secret, error) {
	path := sm.secretPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secrets: not found: %s", name)
	}

	var secret Secret
	if err := json.Unmarshal(data, &secret); err != nil {
		return nil, fmt.Errorf("secrets: unmarshal failed: %w", err)
	}

	decrypted, err := decrypt(secret.Value, sm.cipher)
	if err != nil {
		return nil, fmt.Errorf("secrets: decryption failed: %w", err)
	}
	secret.Value = decrypted

	return &secret, nil
}

func (sm *SecretsManager) UpdateSecret(name, value string) error {
	path := sm.secretPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("secrets: not found: %s", name)
	}

	var secret Secret
	if err := json.Unmarshal(data, &secret); err != nil {
		return fmt.Errorf("secrets: unmarshal failed: %w", err)
	}

	encrypted, err := encrypt(value, sm.cipher)
	if err != nil {
		return fmt.Errorf("secrets: encryption failed: %w", err)
	}

	secret.Value = encrypted
	secret.UpdatedAt = time.Now()

	newData, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal failed: %w", err)
	}

	if err := os.WriteFile(path, newData, 0600); err != nil {
		return fmt.Errorf("secrets: write failed: %w", err)
	}

	return nil
}

func (sm *SecretsManager) DeleteSecret(name string) error {
	path := sm.secretPath(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("secrets: not found: %s", name)
	}
	return os.Remove(path)
}

func (sm *SecretsManager) ListSecrets() ([]*Secret, error) {
	var secrets []*Secret

	entries, err := os.ReadDir(sm.secretsDir)
	if err != nil {
		return nil, fmt.Errorf("secrets: failed to read dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(sm.secretsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var secret Secret
		if err := json.Unmarshal(data, &secret); err != nil {
			continue
		}

		secret.Value = ""
		secrets = append(secrets, &secret)
	}

	return secrets, nil
}

func (sm *SecretsManager) MountSecret(name, containerPath string, mode os.FileMode) error {
	secret, err := sm.GetSecret(name)
	if err != nil {
		return err
	}

	dir := filepath.Dir(containerPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("secrets: failed to create mount dir: %w", err)
	}

	if err := os.WriteFile(containerPath, []byte(secret.Value), mode); err != nil {
		return fmt.Errorf("secrets: failed to write mount file: %w", err)
	}

	return nil
}

func (sm *SecretsManager) AsEnvVars(names []string) ([]string, error) {
	var envVars []string

	for _, name := range names {
		secret, err := sm.GetSecret(name)
		if err != nil {
			return nil, fmt.Errorf("secrets: failed to get %s: %w", name, err)
		}
		envVars = append(envVars, fmt.Sprintf("%s=%s", secret.Name, secret.Value))
	}

	return envVars, nil
}

func (sm *SecretsManager) ExportSecrets(path string) error {
	secrets, err := sm.ListSecrets()
	if err != nil {
		return err
	}

	for _, s := range secrets {
		secret, err := sm.GetSecret(s.Name)
		if err != nil {
			continue
		}
		s.Value = secret.Value
	}

	data, err := json.MarshalIndent(secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: marshal failed: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

func (sm *SecretsManager) ImportSecrets(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("secrets: read failed: %w", err)
	}

	var secrets []*Secret
	if err := json.Unmarshal(data, &secrets); err != nil {
		return fmt.Errorf("secrets: unmarshal failed: %w", err)
	}

	for _, secret := range secrets {
		if err := sm.CreateSecret(secret.Name, secret.Value, secret.Type, secret.Labels); err != nil {
			return fmt.Errorf("secrets: import failed for %s: %w", secret.Name, err)
		}
	}

	return nil
}

func (sm *SecretsManager) secretPath(name string) string {
	return filepath.Join(sm.secretsDir, name+".json")
}

func encrypt(plaintext string, gcm cipher.AEAD) (string, error) {
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decrypt(ciphertext string, gcm cipher.AEAD) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func deriveKey(password string) []byte {
	hash := sha256.Sum256([]byte(password))
	return hash[:]
}
