// Package config manages the z-api-proxy configuration.
//
// Configuration is split across two files in %APPDATA%\Z-API-Proxy:
//
//   - config.toml: non-sensitive settings (listen address, model mappings,
//     tunnel mode, worker name, etc.)
//   - secrets.toml: sensitive values (API keys, tokens) — never written
//     to config.toml, can be excluded from backups and version control.
//
// The Manager type loads both files, merges them, and hot-reloads when
// either file changes on disk (polled every 5 seconds).
//
// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the root configuration structure, deserialized from config.toml.
// Sensitive fields (APIKey, APIToken, TunnelToken) are loaded from
// secrets.toml and merged in at runtime.
type Config struct {
	Server     ServerConfig     `toml:"server"`
	Upstream   UpstreamConfig   `toml:"upstream"`
	Tunnel     TunnelConfig     `toml:"tunnel"`
	Security   SecurityConfig   `toml:"security"`
	Cloudflare CloudflareConfig `toml:"cloudflare"`
	Models     []ModelMapping   `toml:"models"`
}

// secretsFile is the structure of secrets.toml — contains only sensitive
// values. Keys mirror the config.toml paths so the user can find them.
type secretsFile struct {
	Upstream struct {
		APIKey string `toml:"api_key"`
	} `toml:"upstream"`
	Tunnel struct {
		Token string `toml:"token"`
	} `toml:"tunnel"`
	Cloudflare struct {
		APIToken string `toml:"api_token"`
	} `toml:"cloudflare"`
}

// ServerConfig holds the local HTTP server settings.
type ServerConfig struct {
	Listen string `toml:"listen"`
}

// UpstreamConfig holds the destination API settings.
// APIKey is populated from secrets.toml, not config.toml.
type UpstreamConfig struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"-"` // injected from secrets.toml
}

// TunnelConfig holds optional Cloudflare Named Tunnel settings.
// Token is populated from secrets.toml.
type TunnelConfig struct {
	Mode     string `toml:"mode"`
	Token    string `toml:"-"` // injected from secrets.toml
	Hostname string `toml:"hostname"`
}

// SecurityConfig holds optional request-validation settings.
type SecurityConfig struct {
	VerifyKey bool `toml:"verify_key"`
}

// CloudflareConfig holds settings for deploying a Cloudflare Worker.
// APIToken is populated from secrets.toml.
type CloudflareConfig struct {
	AccountID  string `toml:"account_id"`
	APIToken   string `toml:"-"` // injected from secrets.toml
	WorkerName string `toml:"worker_name"`
}

// ModelMapping defines a single bidirectional model-names translation.
type ModelMapping struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

// Load reads and parses the TOML configuration file at path.
// Missing defaults are filled in.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8787"
	}
	if cfg.Upstream.BaseURL == "" {
		cfg.Upstream.BaseURL = "https://api.z.ai/api/coding/paas/v4"
	}
	if cfg.Tunnel.Mode == "" {
		cfg.Tunnel.Mode = "quick"
	}
	if cfg.Cloudflare.WorkerName == "" {
		cfg.Cloudflare.WorkerName = "z-api-proxy"
	}
	return &cfg, nil
}

// loadSecrets reads secrets.toml and returns the parsed secrets.
func loadSecrets(path string) (*secretsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return &secretsFile{}, nil // missing file is OK
	}
	var s secretsFile
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// mergeSecrets injects secrets.toml values into a Config.
func mergeSecrets(cfg *Config, sec *secretsFile) {
	cfg.Upstream.APIKey = sec.Upstream.APIKey
	cfg.Tunnel.Token = sec.Tunnel.Token
	cfg.Cloudflare.APIToken = sec.Cloudflare.APIToken
}

// ForwardMap returns a lookup from Cursor model names to upstream model names.
func (c *Config) ForwardMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.From] = mm.To
	}
	return m
}

// ReverseMap returns a lookup from upstream model names back to Cursor names.
func (c *Config) ReverseMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.To] = mm.From
	}
	return m
}

// CreateDefault writes a starter config.toml with all known z.ai model
// mappings. Secrets are NOT written here — they go in secrets.toml.
func CreateDefault(path string) error {
	content := `# Z-API Proxy Configuration
# Sensitive values (api_key, api_token, tunnel token) go in secrets.toml,
# not this file. See DefaultSecretsPath() for the location.

[server]
# Local listen address. Set this as the custom OpenAI base URL in Cursor.
listen = "127.0.0.1:8787"

[upstream]
# z.ai API base URL
base_url = "https://api.z.ai/api/coding/paas/v4"

# Cloudflare tunnel settings.
# mode = "quick" (default): random ephemeral URL, no account needed.
# mode = "named":            stable URL, requires token in secrets.toml.
[tunnel]
mode = "quick"
hostname = ""

# Security: when true and api_key is set in secrets.toml, only requests
# with the matching key are accepted. Others get 401.
[security]
verify_key = false

# Cloudflare Worker deployment settings.
# Deploy a stable Worker proxy via tray menu → Deploy Cloudflare Worker.
# account_id from dash.cloudflare.com (right sidebar).
# api_token goes in secrets.toml.
[cloudflare]
account_id = ""
worker_name = "z-api-proxy"

# Model name mappings.
# Cursor sends "from", proxy rewrites to "to" before forwarding upstream.

# GLM-5 family
[[models]]
from = "z.ai/glm-5.2"
to = "glm-5.2"

[[models]]
from = "z.ai/glm-5.1"
to = "glm-5.1"

[[models]]
from = "z.ai/glm-5"
to = "glm-5"

[[models]]
from = "z.ai/glm-5-turbo"
to = "glm-5-turbo"

[[models]]
from = "z.ai/glm-5v-turbo"
to = "glm-5v-turbo"

# GLM-4.7 family
[[models]]
from = "z.ai/glm-4.7"
to = "glm-4.7"

[[models]]
from = "z.ai/glm-4.7-flash"
to = "glm-4.7-flash"

[[models]]
from = "z.ai/glm-4.7-flashx"
to = "glm-4.7-flashx"

# GLM-4.6 family
[[models]]
from = "z.ai/glm-4.6"
to = "glm-4.6"

[[models]]
from = "z.ai/glm-4.6v"
to = "glm-4.6v"

# GLM-4.5 family
[[models]]
from = "z.ai/glm-4.5"
to = "glm-4.5"

[[models]]
from = "z.ai/glm-4.5-air"
to = "glm-4.5-air"

[[models]]
from = "z.ai/glm-4.5-flash"
to = "glm-4.5-flash"

[[models]]
from = "z.ai/glm-4.5v"
to = "glm-4.5v"`
	return os.WriteFile(path, []byte(content), 0600)
}

// CreateDefaultSecrets writes a starter secrets.toml with commented-out
// placeholders so the user knows the format.
func CreateDefaultSecrets(path string) error {
	content := `# Z-API Proxy Secrets
# This file contains sensitive values. Keep it private!
# Do NOT commit to version control.

# z.ai API key. Leave empty to pass through from Cursor.
[upstream]
api_key = ""

# Cloudflare Named Tunnel token (from Zero Trust dashboard).
# Only needed when [tunnel].mode = "named" in config.toml.
[tunnel]
token = ""

# Cloudflare API token for Worker deployment.
# Create at dash.cloudflare.com/profile/api-tokens
# with "Workers Scripts: Edit" permission.
[cloudflare]
api_token = ""
`
	return os.WriteFile(path, []byte(content), 0600)
}

// AppConfigDir returns the per-user configuration directory for the proxy,
// creating it if necessary.
func AppConfigDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, ".config")
	}
	dir := filepath.Join(appData, "Z-API-Proxy")
	os.MkdirAll(dir, 0755)
	return dir
}

// DefaultConfigPath returns the canonical location of config.toml.
func DefaultConfigPath() string {
	return filepath.Join(AppConfigDir(), "config.toml")
}

// DefaultSecretsPath returns the canonical location of secrets.toml.
func DefaultSecretsPath() string {
	return filepath.Join(AppConfigDir(), "secrets.toml")
}

// LoadWithSecrets loads config.toml and secrets.toml, merges them, and
// returns the combined Config.
func LoadWithSecrets(configPath, secretsPath string) (*Config, error) {
	cfg, err := Load(configPath)
	if err != nil {
		return nil, err
	}
	sec, err := loadSecrets(secretsPath)
	if err != nil {
		return nil, fmt.Errorf("secrets.toml parse error: %w", err)
	}
	mergeSecrets(cfg, sec)
	return cfg, nil
}

// Manager provides thread-safe access to the live Config and hot-reloads
// it when either config.toml or secrets.toml changes on disk.
type Manager struct {
	configPath  string
	secretsPath string
	current     atomic.Pointer[Config]
	configMod   time.Time
	secretsMod  time.Time
}

// NewManager loads (or creates) both config files and starts watching.
func NewManager(configPath string) (*Manager, error) {
	secretsPath := DefaultSecretsPath()

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := CreateDefault(configPath); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(secretsPath); os.IsNotExist(err) {
		if err := CreateDefaultSecrets(secretsPath); err != nil {
			return nil, err
		}
	}

	cfg, err := LoadWithSecrets(configPath, secretsPath)
	if err != nil {
		return nil, err
	}

	cInfo, _ := os.Stat(configPath)
	sInfo, _ := os.Stat(secretsPath)
	m := &Manager{
		configPath:  configPath,
		secretsPath: secretsPath,
		configMod:   cInfo.ModTime(),
		secretsMod:  sInfo.ModTime(),
	}
	m.current.Store(cfg)
	go m.watch()
	return m, nil
}

// Get returns the most recently loaded Config (config + secrets merged).
func (m *Manager) Get() *Config {
	return m.current.Load()
}

// watch polls both files for changes and reloads when either changes.
func (m *Manager) watch() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cInfo, err := os.Stat(m.configPath)
		if err != nil {
			continue
		}
		sInfo, err := os.Stat(m.secretsPath)
		if err != nil {
			continue
		}
		if cInfo.ModTime().After(m.configMod) || sInfo.ModTime().After(m.secretsMod) {
			cfg, err := LoadWithSecrets(m.configPath, m.secretsPath)
			if err == nil {
				m.current.Store(cfg)
				m.configMod = cInfo.ModTime()
				m.secretsMod = sInfo.ModTime()
			}
		}
	}
}
