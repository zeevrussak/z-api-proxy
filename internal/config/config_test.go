package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempConfig writes the given TOML content to a temp file and returns
// its path. The file is cleaned up automatically because it resides in the
// test's t.TempDir().
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTempConfig(t, `
[server]
listen = "0.0.0.0:9999"

[upstream]
base_url = "https://example.com/v1"
api_key = "secret"

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
	if cfg.Upstream.APIKey != "secret" {
		t.Errorf("APIKey = %q, want %q", cfg.Upstream.APIKey, "secret")
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
api_key = "test"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Errorf("default Listen = %q, want 127.0.0.1:8787", cfg.Server.Listen)
	}
	if cfg.Upstream.BaseURL != "https://api.z.ai/api/paas/v4" {
		t.Errorf("default BaseURL = %q, want https://api.z.ai/api/paas/v4", cfg.Upstream.BaseURL)
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
	if fwd["z.ai/glm-4.6"] != "glm-4.6" {
		t.Errorf("ForwardMap[z.ai/glm-4.6] = %q, want glm-4.6", fwd["z.ai/glm-4.6"])
	}
}

func TestReverseMap(t *testing.T) {
	cfg := &Config{
		Models: []ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
			{From: "z.ai/glm-4.6", To: "glm-4.6"},
		},
	}
	rev := cfg.ReverseMap()
	if len(rev) != 2 {
		t.Fatalf("ReverseMap length = %d, want 2", len(rev))
	}
	if rev["glm-5.2"] != "z.ai/glm-5.2" {
		t.Errorf("ReverseMap[glm-5.2] = %q, want z.ai/glm-5.2", rev["glm-5.2"])
	}
	if rev["glm-4.6"] != "z.ai/glm-4.6" {
		t.Errorf("ReverseMap[glm-4.6] = %q, want z.ai/glm-4.6", rev["glm-4.6"])
	}
}

func TestForwardReverseMap_Empty(t *testing.T) {
	cfg := &Config{}
	if m := cfg.ForwardMap(); len(m) != 0 {
		t.Errorf("empty ForwardMap length = %d, want 0", len(m))
	}
	if m := cfg.ReverseMap(); len(m) != 0 {
		t.Errorf("empty ReverseMap length = %d, want 0", len(m))
	}
}

func TestCreateDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := CreateDefault(path); err != nil {
		t.Fatalf("CreateDefault: %v", err)
	}

	// The created file should be loadable and contain the expected defaults.
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after CreateDefault: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Errorf("Listen = %q, want 127.0.0.1:8787", cfg.Server.Listen)
	}
	if cfg.Upstream.BaseURL != "https://api.z.ai/api/paas/v4" {
		t.Errorf("BaseURL = %q, want https://api.z.ai/api/paas/v4", cfg.Upstream.BaseURL)
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("Models length = %d, want 2", len(cfg.Models))
	}
}
