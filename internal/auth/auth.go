//go:build linux

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type AuthConfig struct {
	Secret  string   `json:"secret"`
	Issuer  string   `json:"issuer"`
	Expiry  Duration `json:"expiry"`
	APIKeys []APIKey `json:"api_keys"`
}

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	duration, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	d.Duration = duration
	return nil
}

type APIKey struct {
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Token struct {
	Sub       string    `json:"sub"`
	Issuer    string    `json:"iss"`
	IssuedAt  time.Time `json:"iat"`
	ExpiresAt time.Time `json:"exp"`
	Scopes    []string  `json:"scopes"`
}

type AuthMiddleware struct {
	config    *AuthConfig
	keyPrefix string
}

func NewAuthMiddleware(config *AuthConfig) *AuthMiddleware {
	if config == nil {
		config = &AuthConfig{
			Secret: generateRandomSecret(),
			Issuer: "nanocontainer",
			Expiry: Duration{24 * time.Hour},
		}
	}

	if config.Secret == "" {
		config.Secret = generateRandomSecret()
	}

	if config.Expiry.Duration == 0 {
		config.Expiry.Duration = 24 * time.Hour
	}

	return &AuthMiddleware{
		config:    config,
		keyPrefix: "nc_",
	}
}

func (am *AuthMiddleware) GenerateToken(sub string, scopes []string) (string, error) {
	if sub == "" {
		return "", fmt.Errorf("subject cannot be empty")
	}

	now := time.Now()
	token := &Token{
		Sub:       sub,
		Issuer:    am.config.Issuer,
		IssuedAt:  now,
		ExpiresAt: now.Add(am.config.Expiry.Duration),
		Scopes:    scopes,
	}

	tokenData, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(am.config.Secret))
	mac.Write(tokenData)
	signature := mac.Sum(nil)

	encoded := hex.EncodeToString(tokenData)
	sigHex := hex.EncodeToString(signature)

	return fmt.Sprintf("%s.%s", encoded, sigHex), nil
}

func (am *AuthMiddleware) ValidateToken(tokenStr string) (*Token, error) {
	parts := strings.SplitN(tokenStr, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}

	encodedData := parts[0]
	sigHex := parts[1]

	tokenData, err := hex.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("invalid token encoding: %w", err)
	}

	signature, err := hex.DecodeString(sigHex)
	if err != nil {
		return nil, fmt.Errorf("invalid signature encoding: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(am.config.Secret))
	mac.Write(tokenData)
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(signature, expectedSig) {
		return nil, fmt.Errorf("invalid token signature")
	}

	var token Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, fmt.Errorf("invalid token data: %w", err)
	}

	if time.Now().After(token.ExpiresAt) {
		return nil, fmt.Errorf("token expired")
	}

	if token.Issuer != am.config.Issuer {
		return nil, fmt.Errorf("invalid issuer")
	}

	return &token, nil
}

func (am *AuthMiddleware) ValidateAPIKey(key string) (*APIKey, error) {
	if !strings.HasPrefix(key, am.keyPrefix) {
		return nil, fmt.Errorf("invalid API key format")
	}

	for _, apiKey := range am.config.APIKeys {
		if apiKey.Key == key {
			if !apiKey.ExpiresAt.IsZero() && time.Now().After(apiKey.ExpiresAt) {
				return nil, fmt.Errorf("API key expired")
			}
			return &apiKey, nil
		}
	}

	return nil, fmt.Errorf("invalid API key")
}

func (am *AuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			apiKey := r.URL.Query().Get("api_key")
			if apiKey != "" {
				key, err := am.ValidateAPIKey(apiKey)
				if err != nil {
					http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
					return
				}
				r.Header.Set("X-API-Key-Name", key.Name)
				r.Header.Set("X-API-Key-Scopes", strings.Join(key.Scopes, ","))
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, "Unauthorized: missing authorization", http.StatusUnauthorized)
			return
		}

		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := am.ValidateToken(tokenStr)
			if err != nil {
				http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}
			r.Header.Set("X-User-ID", token.Sub)
			r.Header.Set("X-User-Scopes", strings.Join(token.Scopes, ","))
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(authHeader, "ApiKey ") {
			apiKey := strings.TrimPrefix(authHeader, "ApiKey ")
			key, err := am.ValidateAPIKey(apiKey)
			if err != nil {
				http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}
			r.Header.Set("X-API-Key-Name", key.Name)
			r.Header.Set("X-API-Key-Scopes", strings.Join(key.Scopes, ","))
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "Unauthorized: invalid authorization header", http.StatusUnauthorized)
	})
}

func (am *AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return am.Middleware(next)
}

func (am *AuthMiddleware) RequireScope(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopesStr := r.Header.Get("X-User-Scopes")
		if scopesStr == "" {
			scopesStr = r.Header.Get("X-API-Key-Scopes")
		}

		if scopesStr == "" {
			http.Error(w, "Forbidden: no scopes found", http.StatusForbidden)
			return
		}

		scopes := strings.Split(scopesStr, ",")
		hasScope := false
		for _, s := range scopes {
			if strings.TrimSpace(s) == scope || strings.TrimSpace(s) == "admin" {
				hasScope = true
				break
			}
		}

		if !hasScope {
			http.Error(w, fmt.Sprintf("Forbidden: missing required scope '%s'", scope), http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func GenerateAPIKey(name string, scopes []string, expiry time.Duration) *APIKey {
	key := generateRandomKey("nc_")

	apiKey := &APIKey{
		Name:      name,
		Key:       key,
		Scopes:    scopes,
		CreatedAt: time.Now(),
	}

	if expiry > 0 {
		apiKey.ExpiresAt = time.Now().Add(expiry)
	}

	return apiKey
}

func SaveConfig(config *AuthConfig, path string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0600)
}

func LoadConfig(path string) (*AuthConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config AuthConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &config, nil
}

func generateRandomSecret() string {
	return generateRandomKey("")
}

func generateRandomKey(prefix string) string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return prefix + hex.EncodeToString(bytes)
}
