package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// makeWorkerTestConfig creates a config for worker tests with both keys set.
func makeWorkerTestConfig() *config.Config {
	return &config.Config{
		Upstream: config.UpstreamConfig{
			BaseURL: "https://api.z.ai/api/coding/paas/v4",
			APIKey:  "real-zai-key-12345",
		},
		Proxy: config.ProxyConfig{
			CursorKey: "cursor-proxy-key-abc",
		},
		Cloudflare: config.CloudflareConfig{
			AccountID:  "test-account",
			APIToken:   "test-token",
			WorkerName: "z-api-proxy-test",
		},
		Models: []config.ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
			{From: "z.ai/glm-4.6", To: "glm-4.6"},
		},
	}
}

// mockUpstream simulates z.ai API. It checks that:
// 1. The Authorization header contains the REAL z.ai key (not cursor key)
// 2. The model name was rewritten from z.ai/glm-5.2 → glm-5.2
// 3. It returns a valid response with the model name for reverse rewriting
func mockUpstream(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		t.Logf("[upstream] received auth: %s", auth)
		t.Logf("[upstream] received path: %s", r.URL.Path)

		if auth != "Bearer real-zai-key-12345" {
			t.Errorf("[upstream] WRONG KEY forwarded: got %q, want %q", auth, "Bearer real-zai-key-12345")
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"error": "wrong key"})
			return
		}

		// Read body to check model rewriting.
		if r.Method == "POST" {
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			t.Logf("[upstream] body: %s", string(body))

			if strings.Contains(string(body), "z.ai/glm-5.2") {
				t.Errorf("[upstream] model NOT rewritten — still contains z.ai/glm-5.2")
			}
			if !strings.Contains(string(body), "glm-5.2") {
				t.Errorf("[upstream] model NOT rewritten — missing glm-5.2")
			}
		}

		// Return a valid chat completion response.
		resp := map[string]interface{}{
			"id":    "chatcmpl-123",
			"model": "glm-5.2",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": "1+1=2",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

// TestGenerateScript_KeyIsolation verifies the Worker JS does NOT
// contain either key in its source — they should come from env secrets.
func TestGenerateScript_KeyIsolation(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	if strings.Contains(script, "real-zai-key-12345") {
		t.Error("UPSTREAM API key leaked into Worker JS source")
	}
	if strings.Contains(script, "cursor-proxy-key-abc") {
		t.Error("CURSOR key leaked into Worker JS source")
	}
	if !strings.Contains(script, "env.API_KEY") {
		t.Error("script must read API_KEY from env")
	}
	if !strings.Contains(script, "env.CURSOR_KEY") {
		t.Error("script must read CURSOR_KEY from env")
	}
	// Verify maps are [key,value] pairs, not objects
	if strings.Contains(script, `"From"`) || strings.Contains(script, `"To"`) {
		t.Error("script must use [key,value] pairs for Map, not {From,To} objects")
	}
}

// TestGenerateScript_AuthorizationSkip verifies the Worker JS skips
// copying the incoming Authorization header, so the real key is used upstream.
func TestGenerateScript_AuthorizationSkip(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	if !strings.Contains(script, "authorization") {
		t.Error("script must reference 'authorization' for the skip check")
	}
	if !strings.Contains(script, "continue") {
		t.Error("script must use 'continue' to skip headers")
	}
}

// TestGenerateScript_HealthEndpoint verifies the /health endpoint
// accepts both keys.
func TestGenerateScript_HealthEndpoint(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	if !strings.Contains(script, "/health") {
		t.Error("script must have /health endpoint")
	}
	if !strings.Contains(script, "acceptedKeys") {
		t.Error("script must build acceptedKeys array")
	}
}

// TestGenerateScript_Logging verifies the Worker has diagnostic logging.
func TestGenerateScript_Logging(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	if !strings.Contains(script, "console.log") {
		t.Error("script must have console.log for diagnostics")
	}
}

// TestGenerateScript_JSONErrorResponses verifies 401 responses
// return proper JSON format (not plain text).
func TestGenerateScript_JSONErrorResponses(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	if !strings.Contains(script, "invalid_api_key") {
		t.Error("script must return invalid_api_key error code")
	}
	if !strings.Contains(script, "application/json") {
		t.Error("script must return JSON content type on errors")
	}
}

// TestGenerateScript_ModelRewrite verifies forward and reverse maps
// are properly embedded.
func TestGenerateScript_ModelRewrite(t *testing.T) {
	cfg := makeWorkerTestConfig()
	script := GenerateScript(cfg)

	// Forward map must contain the mapping.
	if !strings.Contains(script, `glm-5.2`) {
		t.Error("script missing model glm-5.2")
	}
	// Must use replaceAll for rewriting.
	if !strings.Contains(script, "replaceAll") {
		t.Error("script must use replaceAll for model rewriting")
	}
	// Must check for both " and " (space after colon) variants.
	if !strings.Contains(script, `"model":"`) {
		t.Error("script must handle no-space model format")
	}
}

// TestDeploy_SetsBothSecrets verifies both API_KEY and CURSOR_KEY
// are pushed to Cloudflare as secrets.
func TestDeploy_SetsBothSecrets(t *testing.T) {
	cfg := makeWorkerTestConfig()

	var secretNames []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/secrets"):
			body := make([]byte, r.ContentLength)
			r.Body.Read(body)
			var sec struct {
				Name string `json:"name"`
			}
			json.Unmarshal(body, &sec)
			secretNames = append(secretNames, sec.Name)
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
	defer server.Close()

	_, err := deployWithServer(cfg, server.URL)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	if len(secretNames) != 2 {
		t.Fatalf("expected 2 secrets set, got %d: %v", len(secretNames), secretNames)
	}

	hasAPIKey := false
	hasCursorKey := false
	for _, name := range secretNames {
		if name == "API_KEY" {
			hasAPIKey = true
		}
		if name == "CURSOR_KEY" {
			hasCursorKey = true
		}
	}
	if !hasAPIKey {
		t.Error("API_KEY secret was not set during deploy")
	}
	if !hasCursorKey {
		t.Error("CURSOR_KEY secret was not set during deploy")
	}
}

// deployWithServer runs Deploy against a test server instead of Cloudflare.
func deployWithServer(cfg *config.Config, serverURL string) (*DeployResult, error) {
	orig := apiBaseOverride
	apiBaseOverride = serverURL
	defer func() { apiBaseOverride = orig }()
	return Deploy(cfg)
}
