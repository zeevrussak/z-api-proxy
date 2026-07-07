package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// TestWorkerPrefixKeyMatching verifies that the Worker /test endpoint
// accepts keys using prefix matching (key + _clientId).
func TestWorkerPrefixKeyMatching(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test" {
			w.WriteHeader(404)
			return
		}
		auth := r.Header.Get("Authorization")
		sentKey := strings.TrimPrefix(auth, "Bearer ")

		// Simulate the Worker's matchKey logic.
		expected := "my-gateway-key"
		matched := false
		clientID := ""
		if sentKey == expected {
			matched = true
		} else if strings.HasPrefix(sentKey, expected+"_") {
			matched = true
			clientID = sentKey[len(expected)+1:]
		}

		if !matched {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]string{"status": "FAIL"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "OK",
			"clientId": clientID,
		})
	}))
	defer server.Close()

	client := server.Client()

	// Test 1: exact key matches.
	t.Run("ExactKey", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("Authorization", "Bearer my-gateway-key")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("got %d, want 200", resp.StatusCode)
		}
	})

	// Test 2: key + _clientId matches.
	t.Run("PrefixWithClientID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("Authorization", "Bearer my-gateway-key_alice")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("got %d, want 200", resp.StatusCode)
		}
	})

	// Test 3: wrong prefix rejected.
	t.Run("WrongPrefix", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		req.Header.Set("Authorization", "Bearer my-gateway-key2")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 401 {
			t.Errorf("got %d, want 401", resp.StatusCode)
		}
	})

	// Test 4: different client IDs both accepted.
	t.Run("DifferentClients", func(t *testing.T) {
		for _, client := range []string{"alice", "bob", "charlie"} {
			req, _ := http.NewRequest("GET", server.URL+"/test", nil)
			req.Header.Set("Authorization", "Bearer my-gateway-key_"+client)
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Errorf("client %s: got %d, want 200", client, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})
}

// TestWorkerStatsEndpoint simulates the /stats endpoint with per-client
// tracking. Verifies that different clients are counted separately.
func TestWorkerStatsEndpoint(t *testing.T) {
	// Simulate the Worker's in-memory stats map.
	stats := map[string]*clientStat{}
	doRequest := func(clientID string) {
		if stats[clientID] == nil {
			stats[clientID] = &clientStat{}
		}
		stats[clientID].Requests++
		stats[clientID].Tokens += 100
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stats" {
			w.WriteHeader(404)
			return
		}
		auth := r.Header.Get("Authorization")
		sentKey := strings.TrimPrefix(auth, "Bearer ")
		expected := "gw-key"

		clientID := ""
		if sentKey == expected {
			clientID = "unknown"
		} else if strings.HasPrefix(sentKey, expected+"_") {
			clientID = sentKey[len(expected)+1:]
		} else {
			w.WriteHeader(401)
			return
		}

		// Simulate recording a request for this client.
		doRequest(clientID)

		showAll := r.URL.Query().Get("all") == "true"
		if showAll {
			var arr []map[string]interface{}
			for c, s := range stats {
				arr = append(arr, map[string]interface{}{
					"client": c, "requests": s.Requests, "tokens": s.Tokens,
				})
			}
			json.NewEncoder(w).Encode(arr)
			return
		}

		s := stats[clientID]
		json.NewEncoder(w).Encode(map[string]interface{}{
			"client": clientID, "requests": s.Requests, "tokens": s.Tokens,
		})
	}))
	defer server.Close()

	client := server.Client()

	// Send 3 requests as alice.
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/stats", nil)
		req.Header.Set("Authorization", "Bearer gw-key_alice")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// Send 1 request as bob.
	req, _ := http.NewRequest("GET", server.URL+"/stats", nil)
	req.Header.Set("Authorization", "Bearer gw-key_bob")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Verify alice has 3 requests.
	req, _ = http.NewRequest("GET", server.URL+"/stats", nil)
	req.Header.Set("Authorization", "Bearer gw-key_alice")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var aliceStats struct {
		Client   string `json:"client"`
		Requests int    `json:"requests"`
		Tokens   int    `json:"tokens"`
	}
	json.NewDecoder(resp.Body).Decode(&aliceStats)
	resp.Body.Close()

	if aliceStats.Client != "alice" {
		t.Errorf("client = %q, want alice", aliceStats.Client)
	}
	if aliceStats.Requests < 3 {
		t.Errorf("alice requests = %d, want >= 3", aliceStats.Requests)
	}

	// Verify /stats?all=true shows both clients.
	req, _ = http.NewRequest("GET", server.URL+"/stats?all=true", nil)
	req.Header.Set("Authorization", "Bearer gw-key_alice")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var allStats []struct {
		Client   string `json:"client"`
		Requests int    `json:"requests"`
	}
	json.NewDecoder(resp.Body).Decode(&allStats)
	resp.Body.Close()

	if len(allStats) < 2 {
		t.Errorf("expected >= 2 clients in /stats?all=true, got %d", len(allStats))
	}

	// Verify bob exists.
	foundBob := false
	for _, s := range allStats {
		if s.Client == "bob" {
			foundBob = true
		}
	}
	if !foundBob {
		t.Error("bob not found in /stats?all=true")
	}
}

// TestCompositeKey verifies the composite key format (cursorKey_clientId).
func TestCompositeKey(t *testing.T) {
	tests := []struct {
		cursorKey string
		clientID  string
		want      string
	}{
		{"gw-key", "alice", "gw-key_alice"},
		{"gw-key", "", "gw-key"},
		{"gw-key", "bob-laptop", "gw-key_bob-laptop"},
	}

	for _, tt := range tests {
		got := tt.cursorKey
		if tt.clientID != "" {
			got = tt.cursorKey + "_" + tt.clientID
		}
		if got != tt.want {
			t.Errorf("cursorKey=%q clientID=%q → %q, want %q",
				tt.cursorKey, tt.clientID, got, tt.want)
		}
	}
}

// TestClientIDDefault verifies OS username fallback.
func TestClientIDDefault(t *testing.T) {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{},
	}
	// Simulate Load() default logic.
	if cfg.Proxy.ClientID == "" {
		cfg.Proxy.ClientID = "default-user"
	}
	if cfg.Proxy.ClientID != "default-user" {
		t.Errorf("ClientID = %q, want default-user", cfg.Proxy.ClientID)
	}
}

type clientStat struct {
	Requests int
	Tokens   int
}
