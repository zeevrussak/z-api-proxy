package config

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Server   ServerConfig   `toml:"server"`
	Upstream UpstreamConfig `toml:"upstream"`
	Models   []ModelMapping `toml:"models"`
}

type ServerConfig struct {
	Listen string `toml:"listen"`
}

type UpstreamConfig struct {
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
}

type ModelMapping struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

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
		cfg.Upstream.BaseURL = "https://api.z.ai/api/paas/v4"
	}
	return &cfg, nil
}

func (c *Config) ForwardMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.From] = mm.To
	}
	return m
}

func (c *Config) ReverseMap() map[string]string {
	m := make(map[string]string, len(c.Models))
	for _, mm := range c.Models {
		m[mm.To] = mm.From
	}
	return m
}

func CreateDefault(path string) error {
	content := `# Z-API Proxy Configuration

[server]
# Local listen address. Set this as the custom OpenAI base URL in Cursor.
listen = "127.0.0.1:8787"

[upstream]
# z.ai API base URL
base_url = "https://api.z.ai/api/paas/v4"

# API key for z.ai. Leave empty to pass through from Cursor.
api_key = ""

# Model name mappings.
# Cursor sends "from", proxy rewrites to "to" before forwarding upstream.
[[models]]
from = "z.ai/glm-5.2"
to = "glm-5.2"

[[models]]
from = "z.ai/glm-4.6"
to = "glm-4.6"
`
	return os.WriteFile(path, []byte(content), 0644)
}

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

func DefaultConfigPath() string {
	return filepath.Join(AppConfigDir(), "config.toml")
}

type Manager struct {
	path    string
	current atomic.Pointer[Config]
	modTime time.Time
}

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

func (m *Manager) Get() *Config {
	return m.current.Load()
}

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
