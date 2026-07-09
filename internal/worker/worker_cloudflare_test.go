package worker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// withAPIBase redirects apiBaseOverride to the given URL for the
// duration of fn, restoring the original value afterward — same
// save/restore pattern as deployWithServer in worker_test.go.
func withAPIBase(t *testing.T, url string, fn func()) {
	t.Helper()
	orig := apiBaseOverride
	apiBaseOverride = url
	defer func() { apiBaseOverride = orig }()
	fn()
}

func cloudflareTestConfig() *config.Config {
	return &config.Config{
		Cloudflare: config.CloudflareConfig{
			AccountID: "acct-123",
			APIToken:  "token-abc",
		},
	}
}

// --- attachCustomDomain -----------------------------------------------

func TestAttachCustomDomain_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/zones"):
			if r.URL.Query().Get("name") != "example.com" {
				t.Errorf("zone lookup queried wrong domain: %q", r.URL.Query().Get("name"))
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": []map[string]string{{"id": "zone-id-1"}},
			})
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/workers/domains"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["zone_id"] != "zone-id-1" {
				t.Errorf("domains request missing correct zone_id: %+v", body)
			}
			if body["hostname"] != "proxy.example.com" {
				t.Errorf("domains request missing correct hostname: %+v", body)
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	client := &http.Client{}
	var url string
	var err error
	withAPIBase(t, server.URL, func() {
		url, err = attachCustomDomain(client, cfg, "my-worker", "proxy.example.com")
	})
	if err != nil {
		t.Fatalf("attachCustomDomain: %v", err)
	}
	if url != "https://proxy.example.com" {
		t.Errorf("url = %q, want https://proxy.example.com", url)
	}
}

func TestAttachCustomDomain_InvalidHostname(t *testing.T) {
	cfg := cloudflareTestConfig()
	client := &http.Client{}
	// No fake server set up at all — function must return early on
	// invalid hostname without making any HTTP call.
	url, err := attachCustomDomain(client, cfg, "my-worker", "localhost")
	if err == nil {
		t.Fatal("expected error for hostname with no dot, got nil")
	}
	if url != "" {
		t.Errorf("expected empty url on error, got %q", url)
	}
}

func TestAttachCustomDomain_ZoneNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/zones") {
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"result": []map[string]string{}})
			return
		}
		t.Errorf("domains API should not be called when zone lookup is empty; got %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	client := &http.Client{}
	var err error
	withAPIBase(t, server.URL, func() {
		_, err = attachCustomDomain(client, cfg, "my-worker", "proxy.nozone.example")
	})
	if err == nil {
		t.Fatal("expected error when zone is not found")
	}
	if !strings.Contains(err.Error(), "nozone.example") {
		t.Errorf("error should mention the domain, got: %v", err)
	}
}

func TestAttachCustomDomain_DomainsAPIRejects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/zones"):
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"result": []map[string]string{{"id": "zone-id-1"}},
			})
		case strings.HasSuffix(r.URL.Path, "/workers/domains"):
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]interface{}{{"code": 123, "message": "bad domain"}},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	client := &http.Client{}
	var err error
	withAPIBase(t, server.URL, func() {
		_, err = attachCustomDomain(client, cfg, "my-worker", "proxy.example.com")
	})
	if err == nil {
		t.Fatal("expected error when domains API rejects the request")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention HTTP status 400, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bad domain") {
		t.Errorf("error should mention Cloudflare's error message, got: %v", err)
	}
}

// --- FetchWorkerStats ---------------------------------------------------

func TestFetchWorkerStats_MissingCredentials(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer server.Close()

	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"missing account id", &config.Config{Cloudflare: config.CloudflareConfig{APIToken: "tok"}}},
		{"missing api token", &config.Config{Cloudflare: config.CloudflareConfig{AccountID: "acct"}}},
		{"missing both", &config.Config{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			withAPIBase(t, server.URL, func() {
				_, err = FetchWorkerStats(tc.cfg)
			})
			if err == nil {
				t.Fatal("expected error for missing credentials")
			}
		})
	}
	if hit {
		t.Error("FetchWorkerStats made an HTTP call despite missing credentials")
	}
}

func TestFetchWorkerStats_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/graphql") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"viewer": map[string]interface{}{
					"accounts": []map[string]interface{}{
						{
							"workersInvocationsAdaptive": []map[string]interface{}{
								{"sum": map[string]interface{}{"requests": 100, "errors": 10, "subrequests": 5}},
								{"sum": map[string]interface{}{"requests": 50, "errors": 5, "subrequests": 2}},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	var stats *WorkerStats
	var err error
	withAPIBase(t, server.URL, func() {
		stats, err = FetchWorkerStats(cfg)
	})
	if err != nil {
		t.Fatalf("FetchWorkerStats: %v", err)
	}
	if stats.TotalRequests != 150 {
		t.Errorf("TotalRequests = %d, want 150", stats.TotalRequests)
	}
	if stats.ErrorCount != 15 {
		t.Errorf("ErrorCount = %d, want 15", stats.ErrorCount)
	}
	if stats.Subrequests != 7 {
		t.Errorf("Subrequests = %d, want 7", stats.Subrequests)
	}
	if stats.SuccessCount != stats.TotalRequests-stats.ErrorCount {
		t.Errorf("SuccessCount = %d, want %d", stats.SuccessCount, stats.TotalRequests-stats.ErrorCount)
	}
}

func TestFetchWorkerStats_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{},
			"errors": []map[string]interface{}{
				{"message": "rate limited by analytics API"},
			},
		})
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	var err error
	withAPIBase(t, server.URL, func() {
		_, err = FetchWorkerStats(cfg)
	})
	if err == nil {
		t.Fatal("expected error for GraphQL-level error")
	}
	if !strings.Contains(err.Error(), "rate limited by analytics API") {
		t.Errorf("error should contain GraphQL error message, got: %v", err)
	}
}

func TestFetchWorkerStats_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	cfg := cloudflareTestConfig()
	var err error
	withAPIBase(t, server.URL, func() {
		_, err = FetchWorkerStats(cfg)
	})
	if err == nil {
		t.Fatal("expected error for non-200 HTTP status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention HTTP status 500, got: %v", err)
	}
}

func TestFetchWorkerStats_NoTraffic(t *testing.T) {
	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "empty workersInvocationsAdaptive",
			body: map[string]interface{}{
				"data": map[string]interface{}{
					"viewer": map[string]interface{}{
						"accounts": []map[string]interface{}{
							{"workersInvocationsAdaptive": []map[string]interface{}{}},
						},
					},
				},
			},
		},
		{
			name: "empty accounts",
			body: map[string]interface{}{
				"data": map[string]interface{}{
					"viewer": map[string]interface{}{
						"accounts": []map[string]interface{}{},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				json.NewEncoder(w).Encode(tc.body)
			}))
			defer server.Close()

			cfg := cloudflareTestConfig()
			var stats *WorkerStats
			var err error
			withAPIBase(t, server.URL, func() {
				stats, err = FetchWorkerStats(cfg)
			})
			if err != nil {
				t.Fatalf("FetchWorkerStats: %v", err)
			}
			if stats == nil {
				t.Fatal("expected non-nil stats for no-traffic response")
			}
			if stats.TotalRequests != 0 || stats.SuccessCount != 0 || stats.ErrorCount != 0 || stats.Subrequests != 0 {
				t.Errorf("expected all-zero stats, got %+v", stats)
			}
		})
	}
}

// --- TestDeployedWorker ---------------------------------------------------

func TestTestDeployedWorker_Success(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	// Determine the key that will be persisted, so we can assert the
	// request the fake server receives matches it exactly.
	wantKey, err := loadOrCreateTestKey()
	if err != nil {
		t.Fatalf("loadOrCreateTestKey: %v", err)
	}

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test" {
			w.WriteHeader(404)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer server.Close()

	if err := TestDeployedWorker(server.URL); err != nil {
		t.Fatalf("TestDeployedWorker: %v", err)
	}
	want := "Bearer " + wantKey
	if gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
	if !strings.HasPrefix(wantKey, "testkey_") {
		t.Errorf("persisted test key %q missing testkey_ prefix", wantKey)
	}
}

func TestTestDeployedWorker_NonOKStatus(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	err := TestDeployedWorker(server.URL)
	if err == nil {
		t.Fatal("expected error for non-200 /test response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention HTTP status 401, got: %v", err)
	}
}

func TestTestDeployedWorker_Unreachable(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	err := TestDeployedWorker("http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable worker")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should mention worker unreachable, got: %v", err)
	}
}

func TestTestDeployedWorker_ReusesSameKeyAcrossCalls(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		w.WriteHeader(200)
	}))
	defer server.Close()

	if err := TestDeployedWorker(server.URL); err != nil {
		t.Fatalf("TestDeployedWorker (first): %v", err)
	}
	if err := TestDeployedWorker(server.URL); err != nil {
		t.Fatalf("TestDeployedWorker (second): %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(keys))
	}
	if keys[0] != keys[1] {
		t.Errorf("test key differed across calls: %q != %q", keys[0], keys[1])
	}
}

// --- small helpers: jsEscape / truncate / cfResponse.ErrorString --------

func TestJsEscape(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no special chars", "hello world", "hello world"},
		{"single quote", "it's fine", `it\'s fine`},
		{"backslash", `C:\path\to\file`, `C:\\path\\to\\file`},
		{"backslash and quote", `it's a \path\`, `it\'s a \\path\\`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsEscape(tc.in)
			if got != tc.want {
				t.Errorf("jsEscape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"shorter than maxLen", "short", 10, "short"},
		{"longer than maxLen", "this is a long string", 10, "this is a ..."},
		{"exact length boundary", "1234567890", 10, "1234567890"},
		{"one over boundary", "12345678901", 10, "1234567890..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.s, tc.maxLen)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.s, tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestCfResponse_ErrorString(t *testing.T) {
	zero := &cfResponse{}
	if got := zero.ErrorString(); got != "unknown error" {
		t.Errorf("zero errors: ErrorString() = %q, want %q", got, "unknown error")
	}

	one := &cfResponse{}
	json.Unmarshal([]byte(`{"errors":[{"code":123,"message":"bad domain"}]}`), one)
	if got := one.ErrorString(); got != "[123] bad domain" {
		t.Errorf("one error: ErrorString() = %q, want %q", got, "[123] bad domain")
	}

	multi := &cfResponse{}
	json.Unmarshal([]byte(`{"errors":[{"code":1,"message":"first"},{"code":2,"message":"second"}]}`), multi)
	want := "[1] first; [2] second"
	if got := multi.ErrorString(); got != want {
		t.Errorf("multiple errors: ErrorString() = %q, want %q", got, want)
	}
}