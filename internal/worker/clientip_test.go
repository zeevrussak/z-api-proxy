package worker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
)

// withExternalIP redirects fetchExternalIPOverride to return a fixed
// IP (or error) for the duration of fn, restoring the original value
// afterward — mirrors withAPIBase's save/restore pattern.
func withExternalIP(t *testing.T, ip string, err error, fn func()) {
	t.Helper()
	orig := fetchExternalIPOverride
	fetchExternalIPOverride = func() (string, error) { return ip, err }
	defer func() { fetchExternalIPOverride = orig }()
	fn()
}

func clientIPTestConfig(enabled bool, apiToken string) *config.Config {
	return &config.Config{
		Cloudflare: config.CloudflareConfig{
			APIToken:           apiToken,
			AutoUpdateClientIP: enabled,
		},
	}
}

// tokenAPIServer builds a fake Cloudflare API implementing
// /user/tokens/verify, GET /user/tokens/{id}, and PUT
// /user/tokens/{id}, recording every PUT body it receives.
func tokenAPIServer(t *testing.T, tokenID string, existingNotIn []string, failVerify, failGet, failPut bool) (*httptest.Server, *[]tokenDefinition) {
	t.Helper()
	var putBodies []tokenDefinition

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/user/tokens/verify":
			if failVerify {
				w.WriteHeader(401)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"errors":  []map[string]interface{}{{"code": 9109, "message": "invalid token"}},
				})
				return
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  map[string]string{"id": tokenID, "status": "active"},
			})

		case r.Method == "GET" && r.URL.Path == "/user/tokens/"+tokenID:
			if failGet {
				w.WriteHeader(500)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"errors":  []map[string]interface{}{{"code": 1000, "message": "internal error"}},
				})
				return
			}
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id":       tokenID,
					"name":     "z-api-proxy token",
					"status":   "active",
					"policies": []map[string]interface{}{{"id": "pol-1", "effect": "allow"}},
					"condition": map[string]interface{}{
						"request_ip": map[string]interface{}{
							"in":     []string{"198.51.100.1/32"},
							"not_in": existingNotIn,
						},
					},
				},
			})

		case r.Method == "PUT" && r.URL.Path == "/user/tokens/"+tokenID:
			if failPut {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"errors":  []map[string]interface{}{{"code": 1001, "message": "bad request"}},
				})
				return
			}
			var def tokenDefinition
			if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
				t.Errorf("cannot decode PUT body: %v", err)
			}
			putBodies = append(putBodies, def)
			w.WriteHeader(200)
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": map[string]string{"id": tokenID}})

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	return server, &putBodies
}

// --- UpdateClientIPIfChanged: gating -----------------------------------

func TestUpdateClientIPIfChanged_DisabledNoOp(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := clientIPTestConfig(false, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "203.0.113.9", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err != nil {
		t.Fatalf("expected nil error when disabled, got: %v", err)
	}
	if hit {
		t.Error("UpdateClientIPIfChanged made an HTTP call despite feature being disabled")
	}
	if _, statErr := os.Stat(externalIPPrefPath()); statErr == nil {
		t.Error("pref file should not be written when feature is disabled")
	}
}

func TestUpdateClientIPIfChanged_NoAPITokenNoOp(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := clientIPTestConfig(true, "")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "203.0.113.9", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err != nil {
		t.Fatalf("expected nil error when api_token unset, got: %v", err)
	}
	if hit {
		t.Error("UpdateClientIPIfChanged made an HTTP call despite missing api_token")
	}
}

// --- UpdateClientIPIfChanged: first run --------------------------------

func TestUpdateClientIPIfChanged_FirstRun_UpdatesAndPersists(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	server, puts := tokenAPIServer(t, "tok-id-1", []string{"203.0.113.100/32"}, false, false, false)
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.42", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err != nil {
		t.Fatalf("UpdateClientIPIfChanged (first run): %v", err)
	}
	if len(*puts) != 1 {
		t.Fatalf("expected exactly 1 PUT call on first run, got %d", len(*puts))
	}
	got := (*puts)[0]
	if got.Condition == nil || got.Condition.RequestIP == nil {
		t.Fatal("PUT body missing condition.request_ip")
	}
	if want := []string{"198.51.100.42/32"}; !equalStrSlices(got.Condition.RequestIP.In, want) {
		t.Errorf("PUT condition.request_ip.in = %v, want %v", got.Condition.RequestIP.In, want)
	}
	if want := []string{"203.0.113.100/32"}; !equalStrSlices(got.Condition.RequestIP.NotIn, want) {
		t.Errorf("PUT condition.request_ip.not_in = %v, want %v (should preserve existing not_in)", got.Condition.RequestIP.NotIn, want)
	}
	if got.Name != "z-api-proxy token" {
		t.Errorf("PUT body dropped name field: got %q", got.Name)
	}

	if lastIP := loadLastKnownIP(); lastIP != "198.51.100.42" {
		t.Errorf("persisted IP = %q, want %q", lastIP, "198.51.100.42")
	}
}

// --- UpdateClientIPIfChanged: unchanged IP -----------------------------

func TestUpdateClientIPIfChanged_UnchangedIP_NoAPICall(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := saveLastKnownIP("198.51.100.42"); err != nil {
		t.Fatalf("saveLastKnownIP: %v", err)
	}

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.42", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err != nil {
		t.Fatalf("expected nil error for unchanged IP, got: %v", err)
	}
	if hit {
		t.Error("UpdateClientIPIfChanged made an HTTP call despite unchanged external IP")
	}
}

// --- UpdateClientIPIfChanged: IP changed -------------------------------

func TestUpdateClientIPIfChanged_IPChanged_UpdatesAndPersists(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := saveLastKnownIP("198.51.100.42"); err != nil {
		t.Fatalf("saveLastKnownIP: %v", err)
	}

	server, puts := tokenAPIServer(t, "tok-id-2", nil, false, false, false)
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.99", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err != nil {
		t.Fatalf("UpdateClientIPIfChanged (IP changed): %v", err)
	}
	if len(*puts) != 1 {
		t.Fatalf("expected exactly 1 PUT call, got %d", len(*puts))
	}
	if want := []string{"198.51.100.99/32"}; !equalStrSlices((*puts)[0].Condition.RequestIP.In, want) {
		t.Errorf("PUT condition.request_ip.in = %v, want %v", (*puts)[0].Condition.RequestIP.In, want)
	}
	if lastIP := loadLastKnownIP(); lastIP != "198.51.100.99" {
		t.Errorf("persisted IP = %q, want %q", lastIP, "198.51.100.99")
	}
}

// --- UpdateClientIPIfChanged: failure modes ----------------------------

func TestUpdateClientIPIfChanged_ExternalIPFetchFails(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "", errFakeNetwork, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err == nil {
		t.Fatal("expected error when external IP lookup fails")
	}
	if hit {
		t.Error("Cloudflare API should not be called if external IP lookup fails")
	}
	if _, statErr := os.Stat(externalIPPrefPath()); statErr == nil {
		t.Error("pref file should not be written when IP lookup fails")
	}
}

func TestUpdateClientIPIfChanged_VerifyFails_NoUpdatePersisted(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	server, puts := tokenAPIServer(t, "tok-id-3", nil, true, false, false)
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.42", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err == nil {
		t.Fatal("expected error when verify fails")
	}
	if len(*puts) != 0 {
		t.Error("PUT should never be called if verify fails")
	}
	if loadLastKnownIP() != "" {
		t.Error("IP should not be persisted when verify fails, so the next poll retries")
	}
}

func TestUpdateClientIPIfChanged_GetFails_NoUpdatePersisted(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	server, puts := tokenAPIServer(t, "tok-id-4", nil, false, true, false)
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.42", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err == nil {
		t.Fatal("expected error when GET token definition fails")
	}
	if len(*puts) != 0 {
		t.Error("PUT should never be called if GET fails")
	}
	if loadLastKnownIP() != "" {
		t.Error("IP should not be persisted when GET fails, so the next poll retries")
	}
}

func TestUpdateClientIPIfChanged_PutFails_TokenAndIPLeftUnchanged(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	server, puts := tokenAPIServer(t, "tok-id-5", nil, false, false, true)
	defer server.Close()

	cfg := clientIPTestConfig(true, "test-token")
	var err error
	withAPIBase(t, server.URL, func() {
		withExternalIP(t, "198.51.100.42", nil, func() {
			err = UpdateClientIPIfChanged(cfg)
		})
	})
	if err == nil {
		t.Fatal("expected error when PUT fails")
	}
	if !strings.Contains(err.Error(), "token left unchanged") {
		t.Errorf("error should reassure the token was left unchanged, got: %v", err)
	}
	if len(*puts) != 0 {
		t.Error("recorded PUT bodies should be empty since the fake server rejects the PUT before recording it")
	}
	if loadLastKnownIP() != "" {
		t.Error("IP should not be persisted when PUT fails, so the next poll retries")
	}
}

// --- parseTraceIP -------------------------------------------------------

func TestParseTraceIP(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{
			name: "typical trace response",
			body: "fl=123abc\nh=www.cloudflare.com\nip=203.0.113.5\nts=1234567890.123\nvisit_scheme=https\n",
			want: "203.0.113.5",
		},
		{
			name: "ipv6",
			body: "fl=123abc\nip=2606:4700:4700::1111\nts=1234567890.123\n",
			want: "2606:4700:4700::1111",
		},
		{
			name:    "no ip line",
			body:    "fl=123abc\nts=1234567890.123\n",
			wantErr: true,
		},
		{
			name:    "empty ip value",
			body:    "fl=123abc\nip=\nts=1234567890.123\n",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTraceIP(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ip=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTraceIP: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseTraceIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- helpers -------------------------------------------------------------

var errFakeNetwork = fmt.Errorf("fake network error")

func equalStrSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
