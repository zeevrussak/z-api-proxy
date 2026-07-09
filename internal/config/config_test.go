package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestLoad_VerifyKeyDefaultsTrue(t *testing.T) {
	path := writeTempConfig(t, `
[upstream]
base_url = "https://test.com"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if !cfg.Security.VerifyKey {
		t.Errorf("VerifyKey = false, want true (absent from config.toml should default true)")
	}
}

// TestLoad_VerifyKeyAlwaysTrue documents the intentional, security-first
// behavior change: an explicit `verify_key = false` in config.toml is
// overridden to true on load. A plain TOML bool can't distinguish
// "absent" from "explicitly false" (Go zero value is false either way),
// so there is no way to default new installs to true without also
// re-enabling it for existing installs that had it explicitly disabled.
// See the comment above `cfg.Security.VerifyKey = true` in config.go.
func TestLoad_VerifyKeyAlwaysTrue(t *testing.T) {
	path := writeTempConfig(t, `
[upstream]
base_url = "https://test.com"

[security]
verify_key = false
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if !cfg.Security.VerifyKey {
		t.Errorf("VerifyKey = false, want true (verify_key is force-enabled regardless of config.toml value)")
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
	if len(cfg.Models) != 19 {
		t.Fatalf("Models length = %d, want 19", len(cfg.Models))
	}
	// config.toml should NOT contain api_key anymore
	if cfg.Upstream.APIKey != "" {
		t.Errorf("APIKey should be empty in config.toml, got %q", cfg.Upstream.APIKey)
	}
	if !cfg.Security.VerifyKey {
		t.Errorf("VerifyKey = false, want true for a freshly generated config.toml")
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

func TestLoadWithSecrets_MalformedSecretsReturnsError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`
[upstream]
base_url = "https://api.z.ai"
`), 0644); err != nil {
		t.Fatal(err)
	}
	// Deliberately malformed (unterminated string) — distinct from the
	// "missing file is OK" case already covered by
	// TestLoadWithSecrets_NoSecretsFile.
	secPath := filepath.Join(dir, "secrets.toml")
	if err := os.WriteFile(secPath, []byte(`
[upstream]
api_key = "unterminated
`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithSecrets(cfgPath, secPath)
	if err == nil {
		t.Fatal("expected error for malformed secrets.toml, got nil")
	}
	if !strings.Contains(err.Error(), "secrets.toml parse error") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "secrets.toml parse error")
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil on error", cfg)
	}
}

// setupManagerTestFiles points APPDATA at a fresh temp dir (so
// DefaultSecretsPath resolves there), writes a matching secrets.toml at
// that resolved location, and writes a config.toml with the given
// content in a separate temp dir. Returns the config.toml path to pass
// to NewManager.
func setupManagerTestFiles(t *testing.T, configContent, secretsContent string) string {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())

	secretsPath := DefaultSecretsPath()
	if err := os.WriteFile(secretsPath, []byte(secretsContent), 0644); err != nil {
		t.Fatalf("write secrets.toml: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	return configPath
}

func TestManager_HotReloadOnConfigChange(t *testing.T) {
	configPath := setupManagerTestFiles(t, `
[server]
listen = "127.0.0.1:9001"

[upstream]
base_url = "https://test.example"
`, `
[upstream]
api_key = "initial-key"
`)

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := mgr.Get().Server.Listen; got != "127.0.0.1:9001" {
		t.Fatalf("initial Listen = %q, want 127.0.0.1:9001", got)
	}

	// Ensure a distinguishable mtime bump on filesystems with coarse
	// mtime resolution before overwriting.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte(`
[server]
listen = "127.0.0.1:9002"

[upstream]
base_url = "https://test.example"
`), 0644); err != nil {
		t.Fatalf("rewrite config.toml: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Get().Server.Listen == "127.0.0.1:9002" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("config hot-reload did not pick up change within 10s — watch() polls every 5s, something is wrong if this exceeds ~10s")
}

func TestManager_HotReloadOnSecretsChange(t *testing.T) {
	appDataDir := t.TempDir()
	t.Setenv("APPDATA", appDataDir)

	secretsPath := DefaultSecretsPath()
	if err := os.WriteFile(secretsPath, []byte(`
[upstream]
api_key = "initial-key"
`), 0644); err != nil {
		t.Fatalf("write secrets.toml: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(`
[server]
listen = "127.0.0.1:9011"

[upstream]
base_url = "https://test.example"
`), 0644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := mgr.Get().Upstream.APIKey; got != "initial-key" {
		t.Fatalf("initial APIKey = %q, want initial-key", got)
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(secretsPath, []byte(`
[upstream]
api_key = "rotated-key"
`), 0644); err != nil {
		t.Fatalf("rewrite secrets.toml: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Get().Upstream.APIKey == "rotated-key" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("secrets hot-reload did not pick up change within 10s — watch() polls every 5s, something is wrong if this exceeds ~10s")
}

// TestManager_MalformedSecretsDoesNotCrashOrClobberLiveConfig verifies
// that a bad reload (malformed secrets.toml written onto a running
// Manager) is a no-op: watch() logs nothing and simply skips the store,
// leaving the last-known-good Config in place. A regression here — e.g.
// panicking, or storing a nil/zero-value Config on a failed reload —
// would be a real production incident (the running proxy would start
// rejecting every request, or crash outright, the moment someone typos
// secrets.toml).
func TestManager_MalformedSecretsDoesNotCrashOrClobberLiveConfig(t *testing.T) {
	configPath := setupManagerTestFiles(t, `
[server]
listen = "127.0.0.1:9021"

[upstream]
base_url = "https://test.example"
`, `
[upstream]
api_key = "good-key"
`)
	secretsPath := DefaultSecretsPath()

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := mgr.Get().Upstream.APIKey; got != "good-key" {
		t.Fatalf("initial APIKey = %q, want good-key", got)
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(secretsPath, []byte(`
[upstream]
api_key = "unterminated
`), 0644); err != nil {
		t.Fatalf("write malformed secrets.toml: %v", err)
	}

	// Poll for long enough to cross one 5s tick boundary (where the bad
	// reload would be attempted and skipped), asserting the good config
	// is never disturbed at any point along the way.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		cfg := mgr.Get()
		if cfg == nil {
			t.Fatal("mgr.Get() returned nil after a malformed secrets.toml reload attempt")
		}
		if cfg.Upstream.APIKey != "good-key" {
			t.Fatalf("mgr.Get().Upstream.APIKey = %q, want it to remain good-key (malformed reload must be a no-op)", cfg.Upstream.APIKey)
		}
		if cfg.Server.Listen != "127.0.0.1:9021" {
			t.Fatalf("mgr.Get().Server.Listen = %q, want it to remain 127.0.0.1:9021 (malformed reload must be a no-op)", cfg.Server.Listen)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestManager_ConcurrentGetDuringReload exercises Manager.Get()'s
// atomic.Pointer safety under real concurrent access while watch() is
// swapping the pointer in the background — mirroring proxy.ServeHTTP,
// which calls manager.Get() on every request and immediately
// dereferences fields on the result. This test is most valuable when
// run with -race (not available in this sandbox, see task notes):
// atomic.Pointer is race-detector-transparent when used correctly, so
// -race can catch misuse in ways a plain liveness check below cannot
// fully prove on its own. Even without -race, this still validates that
// Get() never hands back nil while a reload races concurrent readers.
func TestManager_ConcurrentGetDuringReload(t *testing.T) {
	configPath := setupManagerTestFiles(t, `
[server]
listen = "127.0.0.1:9101"

[upstream]
base_url = "https://test.example"
`, `
[upstream]
api_key = "initial-key"
`)

	mgr, err := NewManager(configPath)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var sawNil atomic.Bool
	var stop atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				cfg := mgr.Get()
				if cfg == nil {
					sawNil.Store(true)
					return
				}
				_ = cfg.Proxy.APIStyle // simulate proxy.go's immediate field dereference on the returned pointer
			}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(configPath, []byte(`
[server]
listen = "127.0.0.1:9102"

[upstream]
base_url = "https://test.example"
`), 0644); err != nil {
		t.Fatalf("rewrite config.toml: %v", err)
	}

	reloaded := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.Get().Server.Listen == "127.0.0.1:9102" {
			reloaded = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	stop.Store(true)
	wg.Wait()

	if !reloaded {
		t.Fatalf("config hot-reload did not pick up change within 10s during concurrent Get() access")
	}
	if sawNil.Load() {
		t.Fatal("mgr.Get() returned nil during concurrent access + reload")
	}
}
