package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// TestLoadOrCreateTestKey_PersistsAndReuses verifies that calling
// loadOrCreateTestKey twice returns the same key (it's cached to disk,
// not regenerated on every call).
func TestLoadOrCreateTestKey_PersistsAndReuses(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	first, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey (first): %v", err)
	}
	if first == "" {
		t.Fatal("generated test key is empty")
	}
	second, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey (second): %v", err)
	}
	if first != second {
		t.Errorf("test key changed between calls: %q != %q", first, second)
	}
}

// TestLoadOrCreateTestKey_UniquePerInstall verifies two separate
// "installs" (distinct APPDATA dirs, e.g. two different machines) get
// different random keys — this is the whole point of replacing the old
// hardcoded shared constant.
func TestLoadOrCreateTestKey_UniquePerInstall(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	keyA, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey (install A): %v", err)
	}

	t.Setenv("APPDATA", t.TempDir())
	keyB, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey (install B): %v", err)
	}

	if keyA == keyB {
		t.Error("two separate installs generated the same test key — not random")
	}
}

// TestLoadOrCreateTestKey_RegeneratesIfMissingOrEmpty covers the upgrade
// path from the old hardcoded constant: no pref file yet, or an empty
// one, must produce a fresh key rather than erroring or crashing.
func TestLoadOrCreateTestKey_RegeneratesIfMissingOrEmpty(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	// No file yet.
	if _, err := os.Stat(testKeyPath()); err == nil {
		t.Fatal("test key file should not exist yet")
	}
	key, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey: %v", err)
	}
	if key == "" {
		t.Fatal("expected a generated key, got empty string")
	}

	// Simulate a corrupted/empty pref file.
	if err := os.WriteFile(testKeyPath(), []byte("   \n"), 0600); err != nil {
		t.Fatalf("write empty pref: %v", err)
	}
	key2, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey after empty file: %v", err)
	}
	if key2 == "" {
		t.Fatal("expected regeneration for empty pref file, got empty string")
	}
}

// TestGenerateTestKey_Format sanity-checks the generated key looks like
// a real secret (non-trivial length, has the expected prefix, and two
// calls never collide).
func TestGenerateTestKey_Format(t *testing.T) {
	k1, err := generateTestKey()
	if err != nil {
		t.Fatalf("generateTestKey: %v", err)
	}
	if !strings.HasPrefix(k1, "testkey_") {
		t.Errorf("generated key %q missing testkey_ prefix", k1)
	}
	if len(k1) < 32 {
		t.Errorf("generated key %q looks too short to be a real secret", k1)
	}
	k2, err := generateTestKey()
	if err != nil {
		t.Fatalf("generateTestKey (second): %v", err)
	}
	if k1 == k2 {
		t.Error("two calls to generateTestKey produced the same value")
	}
}

// TestWorkerTestEndpoint_DeployAndVerify simulates deploying a Worker
// and then calling /test with the per-deployment test key to verify
// the Worker is correctly handling key validation.
func TestWorkerTestEndpoint_DeployAndVerify(t *testing.T) {
	// loadOrCreateTestKey persists to config.AppConfigDir() — sandbox it
	// to a temp dir so tests don't read/write the real user's APPDATA.
	t.Setenv("APPDATA", t.TempDir())
	testKey, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey: %v", err)
	}

	cfg := &config.Config{
		Upstream: config.UpstreamConfig{
			BaseURL: "https://api.z.ai/api/coding/paas/v4",
			APIKey:  "real-zai-key",
		},
		Proxy: config.ProxyConfig{
			CursorKey: "cursor-gateway-key",
		},
		Cloudflare: config.CloudflareConfig{
			AccountID:  "test-account",
			APIToken:   "test-token",
			WorkerName: "z-api-proxy-test",
		},
		Models: []config.ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
		},
	}

	// Simulated Worker server that implements the Worker JS logic.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/test" {
			auth := r.Header.Get("Authorization")
			xKey := r.Header.Get("x-api-key")
			sentKey := strings.TrimPrefix(auth, "Bearer ")
			if sentKey == "" {
				sentKey = xKey
			}

			if sentKey == testKey {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(map[string]string{
					"status":  "OK",
					"matched": "TEST_KEY",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "FAIL",
				"message": "No matching key",
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	// Test 1: Correct test key via Authorization header.
	t.Run("TestKey_BearerAuth", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("Authorization", "Bearer "+testKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		var result struct {
			Status  string `json:"status"`
			Matched string `json:"matched"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		if result.Status != "OK" {
			t.Errorf("status = %q, want OK", result.Status)
		}
		if result.Matched != "TEST_KEY" {
			t.Errorf("matched = %q, want TEST_KEY", result.Matched)
		}
	})

	// Test 2: Correct test key via x-api-key header.
	t.Run("TestKey_XApiKey", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("x-api-key", testKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
	})

	// Test 3: Wrong key returns 401.
	t.Run("WrongKey_Rejected", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("Authorization", "Bearer wrong-key")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Test 4: No key returns 401.
	t.Run("NoKey_Rejected", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 401 {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Test 5: Deploy should set both secrets correctly.
	t.Run("Deploy_SetsSecrets", func(t *testing.T) {
		var secrets []string
		deployServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/secrets"):
				body := make([]byte, r.ContentLength)
				r.Body.Read(body)
				var sec struct {
					Name string `json:"name"`
				}
				json.Unmarshal(body, &sec)
				secrets = append(secrets, sec.Name)
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

			case r.Method == "PUT" && strings.Contains(r.URL.Path, "/workers/scripts/"):
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

			case strings.Contains(r.URL.Path, "/subdomain"):
				if r.Method == "POST" {
					w.WriteHeader(200)
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{
					"result": map[string]string{"subdomain": "test-sub"},
				})

			default:
				w.WriteHeader(200)
			}
		}))
		defer deployServer.Close()

		result, err := deployWithServer(cfg, deployServer.URL)
		if err != nil {
			t.Fatalf("deploy failed: %v", err)
		}
		if result == nil {
			t.Fatal("deploy returned nil result")
		}

		hasAPIKey := false
		hasCursorKey := false
		hasTestKey := false
		for _, s := range secrets {
			if s == "API_KEY" {
				hasAPIKey = true
			}
			if s == "CURSOR_KEY" {
				hasCursorKey = true
			}
			if s == "TEST_KEY" {
				hasTestKey = true
			}
		}
		if !hasAPIKey {
			t.Error("API_KEY secret not set during deploy")
		}
		if !hasCursorKey {
			t.Error("CURSOR_KEY secret not set during deploy")
		}
		if !hasTestKey {
			t.Error("TEST_KEY secret not set during deploy")
		}
	})
}
