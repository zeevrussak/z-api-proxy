package tunnel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withCfAPITestServer wires cfAPIBaseOverride to a local test server and
// restores the original value on cleanup, mirroring
// withCloudflaredTestServers' approach for the download-related overrides.
func withCfAPITestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	orig := cfAPIBaseOverride
	cfAPIBaseOverride = srv.URL
	t.Cleanup(func() {
		srv.Close()
		cfAPIBaseOverride = orig
	})
	return srv
}

// TestCreateNamedTunnelViaAPI_Success fakes every Cloudflare API call
// CreateNamedTunnelViaAPI makes in sequence (create tunnel, configure
// ingress, zone lookup, existing-DNS lookup, DNS record creation) and
// asserts the happy path returns the expected TunnelCreationResult.
func TestCreateNamedTunnelViaAPI_Success(t *testing.T) {
	const tunnelID = "tunnel-abc-123"
	const connectToken = "fake-connect-token"
	const zoneID = "zone-xyz-789"

	withCfAPITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			fmt.Fprintf(w, `{"result":{"id":%q,"connect_token":%q}}`, tunnelID, connectToken)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/configurations"):
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"result":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			fmt.Fprintf(w, `{"result":[{"id":%q,"name":"example.com"}]}`, zoneID)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/dns_records"):
			// No existing record to delete.
			fmt.Fprint(w, `{"result":[]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"result":{}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	result, err := CreateNamedTunnelViaAPI("account-1", "api-token", "proxy.example.com", "127.0.0.1:8787")
	if err != nil {
		t.Fatalf("CreateNamedTunnelViaAPI: %v", err)
	}
	if result.TunnelID != tunnelID {
		t.Errorf("TunnelID = %q, want %q", result.TunnelID, tunnelID)
	}
	if result.Token != connectToken {
		t.Errorf("Token = %q, want %q", result.Token, connectToken)
	}
	if result.Hostname != "https://proxy.example.com" {
		t.Errorf("Hostname = %q, want %q", result.Hostname, "https://proxy.example.com")
	}
}

// TestCreateNamedTunnelViaAPI_ZoneNotFoundReturnsError proves error
// propagation works, not just the happy path: when the zone lookup
// returns no results (domain not present in the Cloudflare account), the
// function must fail with a clear "could not find zone" error rather
// than proceeding with an empty zone ID.
func TestCreateNamedTunnelViaAPI_ZoneNotFoundReturnsError(t *testing.T) {
	withCfAPITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
			fmt.Fprint(w, `{"result":{"id":"tunnel-1","connect_token":"tok"}}`)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/configurations"):
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"result":{}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			// No zone found for this domain.
			fmt.Fprint(w, `{"result":[]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	_, err := CreateNamedTunnelViaAPI("account-1", "api-token", "proxy.example.com", "127.0.0.1:8787")
	if err == nil {
		t.Fatal("expected error when zone lookup returns no results, got nil")
	}
	if !strings.Contains(err.Error(), "could not find zone") {
		t.Errorf("error = %v, want a 'could not find zone' error", err)
	}
}
