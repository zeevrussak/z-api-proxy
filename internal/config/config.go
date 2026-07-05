// Package config manages the z-api-proxy configuration.
//
// The configuration is stored as TOML in a user-specific directory
// (%APPDATA%\Z-API-Proxy on Windows). The Manager type provides
// thread-safe access to the live configuration and hot-reloads changes
// by polling the file modification time every 5 seconds.
package config

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is the root configuration structure, deserialized from the TOML file.
type Config struct {
	Server     ServerConfig     `toml:"server"`
	Upstream   UpstreamConfig   `toml:"upstream"`
	Tunnel     TunnelConfig     `toml:"tunnel"`
	Security   SecurityConfig   `toml:"security"`
	Cloudflare CloudflareConfig `toml:"cloudflare"`
	Models     []ModelMapping   `toml:"models"`
}

// ServerConfig holds the local HTTP server settings.
type ServerConfig struct {
	// Listen is the local address the proxy binds to (e.g. "127.0.0.1:8787").
	Listen string `toml:"listen"`
}

// UpstreamConfig holds the destination API settings.
type UpstreamConfig struct {
	// BaseURL is the z.ai API base URL including the API version path.
	BaseURL string `toml:"base_url"`
	// APIKey is an optional z.ai API key. When non-empty it overrides the
	// Authorization header on upstream requests. When empty the proxy
	// passes through whatever the client (Cursor) sent.
	APIKey string `toml:"api_key"`
}

// TunnelConfig holds optional Cloudflare Named Tunnel settings.
// When Mode is "named" and Token is non-empty, the tunnel uses a stable
// hostname instead of a random Quick Tunnel URL.
type TunnelConfig struct {
	// Mode is "quick" (default, ephemeral URL) or "named" (stable URL).
	Mode string `toml:"mode"`
	// Token is the Cloudflare tunnel token from the Zero Trust dashboard.
	// Required when Mode is "named".
	Token string `toml:"token"`
	// Hostname is the stable public hostname (e.g. proxy.example.com).
	// Used for display/copy when Mode is "named".
	Hostname string `toml:"hostname"`
}

// SecurityConfig holds optional request-validation settings.
type SecurityConfig struct {
	// VerifyKey, when true and Upstream.APIKey is non-empty, requires that
	// incoming requests carry the same key in the Authorization header.
	// Requests with a different or missing key are rejected with 401.
	VerifyKey bool `toml:"verify_key"`
}

// CloudflareConfig holds settings for deploying a Cloudflare Worker
// that acts as a public reverse proxy with stable URL.
type CloudflareConfig struct {
	// AccountID is the Cloudflare account ID (from dashboard right sidebar).
	AccountID string `toml:"account_id"`
	// APIToken is a Cloudflare API token with Workers Edit permission.
	APIToken string `toml:"api_token"`
	// WorkerName is the name for the deployed Worker script.
	// Defaults to "z-api-proxy".
	WorkerName string `toml:"worker_name"`
}

// ModelMapping defines a single bidirectional model-names translation.
// Cursor sends From; the proxy rewrites it to To before forwarding upstream.
// In responses, To is rewritten back to From so Cursor recognizes the model.
type ModelMapping struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

// Load reads and parses the TOML configuration file at path.
// Missing [server].listen and [upstream].base_url values are replaced
// with sensible defaults.
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
	return &cfg, nil
}

// ForwardMap returns a lookup from Cursor model names to upstream model names.
func (c *Config) ForwardMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.From] = mm.To
	}
	return m
}

// ReverseMap returns a lookup from upstream model names back to Cursor
// model names, used when rewriting responses.
func (c *Config) ReverseMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.To] = mm.From
	}
	return m
}

// CreateDefault writes a starter config file with all known z.ai model
// mappings to the given path.
func CreateDefault(path string) error {
	content := `# Z-API Proxy Configuration

[server]
# Local listen address. Set this as the custom OpenAI base URL in Cursor.
listen = "127.0.0.1:8787"

[upstream]
# z.ai API base URL
base_url = "https://api.z.ai/api/coding/paas/v4"

# API key for z.ai. Leave empty to pass through from Cursor.
api_key = ""

# Cloudflare tunnel settings.
# mode = "quick" (default): random ephemeral URL, no account needed.
# mode = "named":            stable URL, requires token from Cloudflare Zero Trust.
[tunnel]
mode = "quick"
token = ""
hostname = ""

# Security: when true and api_key is set, only requests with the matching
# key are accepted. Others get 401.
[security]
verify_key = false

# Cloudflare Worker deployment settings.
# Deploy a stable Worker proxy via tray menu → Deploy Cloudflare Worker.
# Get account_id from dash.cloudflare.com (right sidebar).
# Create api_token at dash.cloudflare.com/profile/api-tokens
# with "Workers Scripts: Edit" permission.
[cloudflare]
account_id = ""
api_token = ""
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
	return os.WriteFile(path, []byte(content), 0644)
}

// AppConfigDir returns the per-user configuration directory for the proxy,
// creating it if necessary. On Windows this is %APPDATA%\Z-API-Proxy;
// on other platforms it falls back to ~/.config/Z-API-Proxy.
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

// DefaultConfigPath returns the canonical location of config.toml inside
// the per-user AppConfigDir.
func DefaultConfigPath() string {
	return filepath.Join(AppConfigDir(), "config.toml")
}

// Manager provides thread-safe access to the live Config and hot-reloads
// it when the file changes on disk. It is safe for concurrent use.
type Manager struct {
	path    string
	current atomic.Pointer[Config]
	modTime time.Time
}

// NewManager loads (or creates) the config at path and starts a background
// goroutine that watches for file changes. Callers should call Get on every
// request to read the freshest configuration.
func NewManager(path string) (*Manager, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := CreateDefault(path); err != nil {
			return nil, err
		}
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	info, _ := os.Stat(path)
	m := &Manager{path: path, modTime: info.ModTime()}
	m.current.Store(cfg)
	go m.watch()
	return m, nil
}

// Get returns the most recently loaded Config. The returned pointer is safe
// for concurrent reads and is never nil after successful initialization.
func (m *Manager) Get() *Config {
	return m.current.Load()
}

// watch polls the config file's modification time every 5 seconds and
// reloads it atomically when a change is detected. Parse errors are
// silently skipped to avoid clobbering a good config with a broken one.
func (m *Manager) watch() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		info, err := os.Stat(m.path)
		if err != nil {
			continue
		}
		if info.ModTime().After(m.modTime) {
			cfg, err := Load(m.path)
			if err == nil {
				m.current.Store(cfg)
				m.modTime = info.ModTime()
			}
		}
	}
}
