// Package store keeps the assistant's own endpoint/model settings. These live
// in %AppData%/pondera/assistant/assistant.json and are deliberately separate
// from Pondera's main config; the API key is stored through the credential
// store under its own variable name.
package store

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pondera/internal/config"
	"pondera/internal/fileutil"
)

// CredentialKey is the credential-store variable that holds the assistant's key.
const CredentialKey = "PONDERA_ASSISTANT_API_KEY"

// Config is the assistant's own persisted configuration.
type Config struct {
	Endpoint      string `json:"endpoint"`
	Kind          string `json:"kind"` // openai | anthropic
	Model         string `json:"model"`
	CredentialKey string `json:"credential_key"`
	UpdatedAt     string `json:"updated_at"`
}

// Store reads and writes the assistant configuration directory.
type Store struct {
	Dir string
}

// New returns a store under %AppData%/pondera/assistant.
func New() *Store {
	home := config.PonderaHomeDir()
	if home == "" {
		return &Store{}
	}
	return &Store{Dir: filepath.Join(home, "assistant")}
}

// Path returns assistant.json's path, or "" when the home dir is unknown.
func (s *Store) Path() string {
	if s == nil || s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "assistant.json")
}

// Load returns the stored settings, or empty settings when absent.
func (s *Store) Load() (Config, error) {
	path := s.Path()
	if path == "" {
		return Config{}, fmt.Errorf("assistant home unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{CredentialKey: CredentialKey}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("assistant config corrupt: %w", err)
	}
	if strings.TrimSpace(cfg.CredentialKey) == "" {
		cfg.CredentialKey = CredentialKey
	}
	return cfg, nil
}

// Save validates and atomically writes the assistant configuration.
func (s *Store) Save(cfg Config) error {
	if s == nil || s.Dir == "" {
		return fmt.Errorf("assistant home unavailable")
	}
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if err := ValidateEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind == "" {
		kind = "openai"
	}
	if kind != "openai" && kind != "anthropic" {
		return fmt.Errorf("不支持的接口类型 %q（只接受 openai / anthropic）", cfg.Kind)
	}
	cfg.Kind = kind
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		return fmt.Errorf("模型名不能为空")
	}
	if cfg.CredentialKey == "" {
		cfg.CredentialKey = CredentialKey
	}
	cfg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(s.Path(), append(raw, '\n'), 0o600)
}

// ValidateEndpoint accepts http(s) with a host and no embedded credentials.
// Localhost http is allowed for self-hosted gateways.
func ValidateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint 不是合法 URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("endpoint 必须是 http(s) URL")
	}
	if parsed.Host == "" {
		return fmt.Errorf("endpoint 缺少主机名")
	}
	if parsed.User != nil {
		return fmt.Errorf("endpoint 不能内嵌账号密码")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("endpoint 不能带 fragment")
	}
	return nil
}

// APIKeySet reports whether a key is present in the credential store without
// exposing its value.
func APIKeySet(cfg Config) bool {
	key := cfg.CredentialKey
	if key == "" {
		key = CredentialKey
	}
	return config.CredentialIsSet(key)
}

// SetAPIKey stores the assistant key through the credential store. It never
// writes .env directly.
func SetAPIKey(cfg Config, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("API key 不能为空")
	}
	if strings.ContainsAny(key, "\r\n") {
		return fmt.Errorf("API key 不能包含换行")
	}
	name := cfg.CredentialKey
	if name == "" {
		name = CredentialKey
	}
	_, err := config.SetCredential(name, key)
	return err
}

// GetAPIKey resolves the assistant key for an outbound request. The value is
// only handed to the HTTP client and must never be logged.
func GetAPIKey(cfg Config) (string, error) {
	name := cfg.CredentialKey
	if name == "" {
		name = CredentialKey
	}
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("助手 API key 未设置")
	}
	return value, nil
}
