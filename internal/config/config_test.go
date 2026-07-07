package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempFile writes content to a temp file and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

// writeTempConfig writes a config.toml to temp dir.
func writeTempConfig(t *testing.T, content string) string {
	return writeTempFile(t, "config.toml", content)
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTempConfig(t, `
[server]
listen = "0.0.0.0:9999"

[upstream]
base_url = "https://example.com/v1"

[[models]]
from = "z.ai/glm-5.2"
to = "glm-5.2"

[[models]]
from = "z.ai/glm-4.6"
to = "glm-4.6"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Server.Listen != "0.0.0.0:9999" {
		t.Errorf("Listen = %q, want %q", cfg.Server.Listen, "0.0.0.0:9999")
	}
	if cfg.Upstream.BaseURL != "https://example.com/v1" {
		t.Errorf("BaseURL = %q, want %q", cfg.Upstream.BaseURL, "https://example.com/v1")
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("Models length = %d, want 2", len(cfg.Models))
	}
	if cfg.Models[0].From != "z.ai/glm-5.2" || cfg.Models[0].To != "glm-5.2" {
		t.Errorf("Models[0] = {%s, %s}, want {z.ai/glm-5.2, glm-5.2}", cfg.Models[0].From, cfg.Models[0].To)
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	path := writeTempConfig(t, `
[upstream]
base_url = "https://test.com"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Errorf("default Listen = %q, want 127.0.0.1:8787", cfg.Server.Listen)
	}
	if cfg.Upstream.BaseURL != "https://test.com" {
		t.Errorf("BaseURL = %q, want https://test.com", cfg.Upstream.BaseURL)
	}
	if cfg.Tunnel.Mode != "quick" {
		t.Errorf("default Tunnel.Mode = %q, want quick", cfg.Tunnel.Mode)
	}
	if cfg.Cloudflare.WorkerName != "z-api-proxy" {
		t.Errorf("default WorkerName = %q, want z-api-proxy", cfg.Cloudflare.WorkerName)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	path := writeTempConfig(t, `this is not = valid = toml [[[[`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestLoadWithSecrets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[upstream]
base_url = "https://api.z.ai"
`), 0644); err != nil {
		t.Fatal(err)
	}
	secPath := filepath.Join(dir, "secrets.toml")
	if err := os.WriteFile(secPath, []byte(`
[upstream]
api_key = "sk-secret-key"

[cloudflare]
api_token = "cf-token-123"

[tunnel]
token = "tunnel-token"
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithSecrets(cfgPath, secPath)
	if err != nil {
		t.Fatalf("LoadWithSecrets: %v", err)
	}
	if cfg.Upstream.APIKey != "sk-secret-key" {
		t.Errorf("APIKey = %q, want sk-secret-key", cfg.Upstream.APIKey)
	}
	if cfg.Cloudflare.APIToken != "cf-token-123" {
		t.Errorf("APIToken = %q, want cf-token-123", cfg.Cloudflare.APIToken)
	}
	if cfg.Tunnel.Token != "tunnel-token" {
		t.Errorf("Tunnel.Token = %q, want tunnel-token", cfg.Tunnel.Token)
	}
}

func TestLoadWithSecrets_NoSecretsFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[upstream]
base_url = "https://api.z.ai"
`), 0644); err != nil {
		t.Fatal(err)
	}
	secPath := filepath.Join(dir, "secrets.toml") // does not exist

	cfg, err := LoadWithSecrets(cfgPath, secPath)
	if err != nil {
		t.Fatalf("LoadWithSecrets with no secrets file: %v", err)
	}
	if cfg.Upstream.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.Upstream.APIKey)
	}
}

func TestForwardMap(t *testing.T) {
	cfg := &Config{
		Models: []ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
			{From: "z.ai/glm-4.6", To: "glm-4.6"},
		},
	}
	fwd := cfg.ForwardMap()
	if len(fwd) != 2 {
		t.Fatalf("ForwardMap length = %d, want 2", len(fwd))
	}
	if fwd["z.ai/glm-5.2"] != "glm-5.2" {
		t.Errorf("ForwardMap[z.ai/glm-5.2] = %q, want glm-5.2", fwd["z.ai/glm-5.2"])
	}
}

func TestReverseMap(t *testing.T) {
	cfg := &Config{
		Models: []ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
		},
	}
	rev := cfg.ReverseMap()
	if rev["glm-5.2"] != "z.ai/glm-5.2" {
		t.Errorf("ReverseMap[glm-5.2] = %q, want z.ai/glm-5.2", rev["glm-5.2"])
	}
}

func TestForwardReverseMap_Empty(t *testing.T) {
	cfg := &Config{}
	if m := cfg.ForwardMap(); len(m) != 0 {
		t.Errorf("empty ForwardMap length = %d, want 0", len(m))
	}
}

func TestCreateDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := CreateDefault(path); err != nil {
		t.Fatalf("CreateDefault: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after CreateDefault: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Errorf("Listen = %q, want 127.0.0.1:8787", cfg.Server.Listen)
	}
	if len(cfg.Models) != 15 {
		t.Fatalf("Models length = %d, want 15", len(cfg.Models))
	}
	// config.toml should NOT contain api_key anymore
	if cfg.Upstream.APIKey != "" {
		t.Errorf("APIKey should be empty in config.toml, got %q", cfg.Upstream.APIKey)
	}
}

func TestCreateDefaultSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.toml")
	if err := CreateDefaultSecrets(path); err != nil {
		t.Fatalf("CreateDefaultSecrets: %v", err)
	}
	// Verify it loads
	sec, err := loadSecrets(path)
	if err != nil {
		t.Fatalf("loadSecrets: %v", err)
	}
	// Defaults should be empty
	if sec.Upstream.APIKey != "" {
		t.Errorf("default APIKey = %q, want empty", sec.Upstream.APIKey)
	}
}
