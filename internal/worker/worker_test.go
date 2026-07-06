package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

func makeTestConfig() *config.Config {
	return &config.Config{
		Upstream: config.UpstreamConfig{
			BaseURL: "https://api.z.ai/api/coding/paas/v4",
			APIKey:  "test-key",
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
}

func TestGenerateScript(t *testing.T) {
	cfg := makeTestConfig()
	script := GenerateScript(cfg)

	if !strings.Contains(script, "UPSTREAM") {
		t.Error("script missing UPSTREAM constant")
	}
	if !strings.Contains(script, "FORWARD_MAP") {
		t.Error("script missing FORWARD_MAP")
	}
	if !strings.Contains(script, "REVERSE_MAP") {
		t.Error("script missing REVERSE_MAP")
	}
	if !strings.Contains(script, "glm-5.2") {
		t.Error("script missing model name")
	}
	if !strings.Contains(script, "env.API_KEY") {
		t.Error("script must read API key from env (Secrets API), not hardcoded")
	}
	if !strings.Contains(script, "export default") {
		t.Error("script must use ES module export")
	}
	if strings.Contains(script, "test-key") {
		t.Error("API key must NOT be embedded in the script source")
	}
}

func TestGenerateScript_JSONParseMaps(t *testing.T) {
	cfg := makeTestConfig()
	cfg.Models = []config.ModelMapping{
		{From: "z.ai/test', alert(1), '", To: "safe"},
	}
	script := GenerateScript(cfg)
	// The model name must be JSON-marshaled (escaped) inside backtick template literals,
	// not raw in JS string context. If unescaped, the comma would break the Map constructor.
	if strings.Contains(script, "z.ai/test',") {
		t.Error("script contains unescaped injection — model names must be JSON-marshaled")
	}
}

func TestDeploy_Success(t *testing.T) {
	deployCalled := false
	secretCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/workers/scripts/z-api-proxy-test") && r.Method == "PUT":
			deployCalled = true
			// Verify Content-Type is multipart.
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "multipart/form-data") {
				t.Errorf("expected multipart, got %s", ct)
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

		case strings.HasSuffix(r.URL.Path, "/secrets") && r.Method == "PUT":
			secretCalled = true
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})

		case strings.Contains(r.URL.Path, "/subdomain"):
			if r.Method == "POST" {
				w.WriteHeader(200)
				return
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]string{"subdomain": "test-sub"},
			})

		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := makeTestConfig()
	result, err := deployWithServer(cfg, server.URL)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}
	if !deployCalled {
		t.Error("deploy API was not called")
	}
	if !secretCalled {
		t.Error("secret API was not called")
	}
	if !strings.Contains(result.URL, "test-sub") {
		t.Errorf("URL = %s, want subdomain 'test-sub'", result.URL)
	}
}

func TestDeploy_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors": []map[string]interface{}{
				{"code": 10000, "message": "Authentication error"},
			},
		})
	}))
	defer server.Close()

	cfg := makeTestConfig()
	_, err := deployWithServer(cfg, server.URL)
	if err == nil {
		t.Fatal("expected error for API failure, got nil")
	}
	if !strings.Contains(err.Error(), "Authentication error") {
		t.Errorf("error should contain Cloudflare message: %v", err)
	}
}

func TestDelete_Success(t *testing.T) {
	deleteCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleteCalled = true
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		}
	}))
	defer server.Close()

	cfg := makeTestConfig()
	err := deleteWithServer(cfg, server.URL)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if !deleteCalled {
		t.Error("delete API was not called")
	}
}

func TestDelete_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors": []map[string]interface{}{
				{"code": 10090, "message": "workers.api_not_found"},
			},
		})
	}))
	defer server.Close()

	cfg := makeTestConfig()
	err := deleteWithServer(cfg, server.URL)
	if err == nil {
		t.Fatal("expected error for non-existent worker")
	}
}

func TestHealthCheck_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-key" {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(200)
			w.Write([]byte("OK"))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	err := HealthCheck(server.URL, "test-key")
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
}

func TestHealthCheck_WrongKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	err := HealthCheck(server.URL, "wrong-key")
	if err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestHealthCheck_Unreachable(t *testing.T) {
	err := HealthCheck("http://127.0.0.1:0", "key")
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

// deployWithServer runs Deploy against a test server instead of Cloudflare.
func deployWithServer(cfg *config.Config, serverURL string) (*DeployResult, error) {
	orig := apiBaseOverride
	apiBaseOverride = serverURL
	defer func() { apiBaseOverride = orig }()
	return Deploy(cfg)
}

// deleteWithServer runs Delete against a test server.
func deleteWithServer(cfg *config.Config, serverURL string) error {
	orig := apiBaseOverride
	apiBaseOverride = serverURL
	defer func() { apiBaseOverride = orig }()
	return Delete(cfg)
}
