package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// TestWorkerTestEndpoint_DeployAndVerify simulates deploying a Worker
// and then calling /test with the built-in test key to verify
// the Worker is correctly handling key validation.
func TestWorkerTestEndpoint_DeployAndVerify(t *testing.T) {
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

			if sentKey == TestKey {
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
		req.Header.Set("Authorization", "Bearer "+TestKey)

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
		req.Header.Set("x-api-key", TestKey)

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
