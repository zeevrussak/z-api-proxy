package tunnel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestHealthCheck_SuccessOn200 covers the straightforward case: the
// tunnel endpoint responds 200 on the very first attempt, so healthCheck
// returns immediately without sleeping through any retries.
func TestHealthCheck_SuccessOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q, want /v1/models", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &Manager{}
	if err := m.healthCheck(srv.URL); err != nil {
		t.Fatalf("healthCheck: %v", err)
	}
}

// TestHealthCheck_Non5xxIsSuccessEvenOn404 locks in a real, easily-missed
// behavioral quirk: healthCheck treats ANY response with status < 500 as
// success, including 404/401. Per the comment above healthCheck in
// tunnel.go, this is deliberate — "Any non-5xx response means the tunnel
// is working (the proxy is receiving requests through the tunnel)". The
// health check can't know whether a valid upstream API key is configured
// without making an authenticated call of its own, so it only verifies
// that traffic reaches the proxy through the tunnel at all, not that the
// proxy is fully configured. A future refactor that "tightens" this to
// require exactly 200 would silently break tunnels fronting proxies that
// return 401/404 for unauthenticated/unknown paths — this test exists to
// catch that regression.
func TestHealthCheck_Non5xxIsSuccessEvenOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	m := &Manager{}
	if err := m.healthCheck(srv.URL); err != nil {
		t.Fatalf("healthCheck should treat HTTP 404 as success (non-5xx), got error: %v", err)
	}
}

// TestHealthCheck_RetriesAfterFirst5xxThenSucceeds proves the retry loop
// itself works (not just the immediate-success path): the server returns
// 500 on the first attempt and 200 from the second attempt onward. This
// incurs exactly one real 2-second sleep (the interval healthCheck sleeps
// between attempts), which is an acceptable, bounded cost for real
// coverage of the retry behavior.
func TestHealthCheck_RetriesAfterFirst5xxThenSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test with a real 2s sleep in -short mode")
	}

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &Manager{}
	if err := m.healthCheck(srv.URL); err != nil {
		t.Fatalf("healthCheck: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Errorf("expected at least 2 attempts, got %d", got)
	}
}

// TestHealthCheck_AllAttemptsFailReturnsTimeoutError exercises the full
// failure path: all 15 attempts return 500, so healthCheck sleeps
// 2s * 14 between attempts (~28-30s total) before giving up. This is real
// wall-clock time for a single assertion on an error message, so it's
// gated behind testing.Short() — `go test -short ./internal/tunnel/...`
// skips it for a fast local dev loop, while a full `go test` (as used for
// final verification) still exercises the real timeout path end-to-end
// rather than relying solely on the fast-path tests above.
func TestHealthCheck_AllAttemptsFailReturnsTimeoutError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ~30s full-timeout healthCheck test in -short mode")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := &Manager{}
	err := m.healthCheck(srv.URL)
	if err == nil {
		t.Fatal("expected error after all attempts fail, got nil")
	}
	if !strings.Contains(err.Error(), "did not become reachable after 30 seconds") {
		t.Errorf("error = %v, want a 'did not become reachable after 30 seconds' message", err)
	}
}
