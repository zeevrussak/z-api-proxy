// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/counter"
)

// --- test helpers -----------------------------------------------------

// buildConfigTOML renders a minimal config.toml with the given API style,
// upstream base URL, and model mappings.
func buildConfigTOML(apiStyle, baseURL string, models []config.ModelMapping) string {
	var sb strings.Builder
	sb.WriteString("[server]\nlisten = \"127.0.0.1:8787\"\n\n")
	fmt.Fprintf(&sb, "[upstream]\nbase_url = %q\n\n", baseURL)
	fmt.Fprintf(&sb, "[proxy]\napi_style = %q\n\n", apiStyle)
	sb.WriteString("[security]\nverify_key = true\n\n")
	for _, m := range models {
		fmt.Fprintf(&sb, "[[models]]\nfrom = %q\nto = %q\n\n", m.From, m.To)
	}
	return sb.String()
}

// buildSecretsTOML renders a minimal secrets.toml with the given upstream
// API key.
func buildSecretsTOML(apiKey string) string {
	return fmt.Sprintf("[upstream]\napi_key = %q\n", apiKey)
}

// newTestProxy builds a Proxy backed by a config.Manager loading a
// hand-written config.toml/secrets.toml pair from a sandboxed temp dir
// (APPDATA is redirected via t.Setenv so no real user state is touched).
func newTestProxy(t *testing.T, apiStyle, baseURL, apiKey string, models []config.ModelMapping) (*Proxy, *counter.Counter) {
	t.Helper()

	tempDir := t.TempDir()
	t.Setenv("APPDATA", tempDir)

	// AppConfigDir() (called by DefaultSecretsPath) creates the
	// %APPDATA%\Z-API-Proxy dir as a side effect.
	secretsPath := config.DefaultSecretsPath()
	if err := os.WriteFile(secretsPath, []byte(buildSecretsTOML(apiKey)), 0600); err != nil {
		t.Fatalf("write secrets.toml: %v", err)
	}

	configPath := filepath.Join(tempDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(buildConfigTOML(apiStyle, baseURL, models)), 0600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	mgr, err := config.NewManager(configPath)
	if err != nil {
		t.Fatalf("config.NewManager: %v", err)
	}

	ctr := counter.New()
	return New(mgr, ctr), ctr
}

func jsonUpstream(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- API style filtering ------------------------------------------------

func TestAPIStyleFiltering(t *testing.T) {
	upstream := jsonUpstream(t, http.StatusOK, `{"ok":true}`)

	tests := []struct {
		name       string
		apiStyle   string
		path       string
		wantStatus int
		wantReject bool
	}{
		{"both allows messages", "both", "/v1/messages", http.StatusOK, false},
		{"both allows chat completions", "both", "/v1/chat/completions", http.StatusOK, false},
		{"openai blocks anthropic messages path", "openai", "/v1/messages", http.StatusForbidden, true},
		{"openai allows chat completions", "openai", "/v1/chat/completions", http.StatusOK, false},
		{"anthropic blocks openai chat completions path", "anthropic", "/v1/chat/completions", http.StatusForbidden, true},
		{"anthropic allows messages", "anthropic", "/v1/messages", http.StatusOK, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			px, ctr := newTestProxy(t, tt.apiStyle, upstream.URL, "", nil)

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			px.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if tt.wantReject {
				if !strings.Contains(rec.Body.String(), `"type":"invalid_request_error"`) {
					t.Errorf("body missing invalid_request_error type: %s", rec.Body.String())
				}
				if got := ctr.Rejected(); got != 1 {
					t.Errorf("Rejected() = %d, want 1", got)
				}
			} else if got := ctr.Rejected(); got != 0 {
				t.Errorf("Rejected() = %d, want 0", got)
			}
		})
	}
}

// --- key verification -----------------------------------------------------

func TestKeyVerification(t *testing.T) {
	upstream := jsonUpstream(t, http.StatusOK, `{"ok":true}`)

	tests := []struct {
		name        string
		upstreamKey string
		authHeader  string
		xAPIKey     string
		wantStatus  int
	}{
		{"no key configured, no auth header", "", "", "", http.StatusOK},
		{"no key configured, garbage auth header still passes", "", "Bearer whatever", "", http.StatusOK},
		{"correct bearer auth", "secret123", "Bearer secret123", "", http.StatusOK},
		{"correct x-api-key", "secret123", "", "secret123", http.StatusOK},
		{"wrong bearer, no x-api-key", "secret123", "Bearer wrong", "", http.StatusUnauthorized},
		{"missing both headers", "secret123", "", "", http.StatusUnauthorized},
		{"x-api-key wins over wrong bearer", "secret123", "Bearer wrong", "secret123", http.StatusOK},
		{"x-api-key wins over correct bearer when x-api-key is wrong", "secret123", "Bearer secret123", "wrongkey", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			px, ctr := newTestProxy(t, "both", upstream.URL, tt.upstreamKey, nil)

			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.xAPIKey != "" {
				req.Header.Set("x-api-key", tt.xAPIKey)
			}
			rec := httptest.NewRecorder()
			px.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if tt.wantStatus == http.StatusUnauthorized {
				if got := ctr.Rejected(); got != 1 {
					t.Errorf("Rejected() = %d, want 1", got)
				}
				if !strings.Contains(rec.Body.String(), "unauthorized: API key mismatch") {
					t.Errorf("unexpected body: %s", rec.Body.String())
				}
			} else if got := ctr.Rejected(); got != 0 {
				t.Errorf("Rejected() = %d, want 0", got)
			}
		})
	}
}

// --- SSE branch -------------------------------------------------------

func TestSSEBranch(t *testing.T) {
	const sseBody = "data: {\"id\":\"chatcmpl-1\",\"model\":\"glm-5.2\",\"choices\":[]}\n" +
		"\n" +
		": keep-alive comment\n" +
		"data: {\"id\":\"chatcmpl-2\",\"model\":\"glm-5.2\",\"choices\":[]}\n" +
		"\n" +
		"data: [DONE]\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sseBody))
		// Real SSE servers stream chunk-by-chunk (Transfer-Encoding:
		// chunked, no Content-Length known up front). Flush explicitly
		// here so the fake upstream matches that contract instead of
		// letting the test server auto-compute a Content-Length for
		// this small, single-write body.
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(upstream.Close)

	models := []config.ModelMapping{{From: "z.ai/glm-5.2", To: "glm-5.2"}}
	px, _ := newTestProxy(t, "both", upstream.URL, "", models)

	server := httptest.NewServer(px)
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := string(body)

	if !strings.Contains(got, `"model":"z.ai/glm-5.2"`) {
		t.Errorf("expected reverse-rewritten model name in data lines, got:\n%s", got)
	}
	if strings.Contains(got, `"model":"glm-5.2"`) {
		t.Errorf("upstream model name leaked unrewritten, got:\n%s", got)
	}
	if !strings.Contains(got, ": keep-alive comment") {
		t.Errorf("non-data comment line was altered, got:\n%s", got)
	}
	if !strings.Contains(got, "data: [DONE]") {
		t.Errorf("[DONE] line missing or altered, got:\n%s", got)
	}
}

// --- regular (non-SSE) branch -------------------------------------------

func TestRegularBranch(t *testing.T) {
	const upstreamBody = `{"id":"chatcmpl-1","model":"glm-5.2","choices":[]}`

	upstream := jsonUpstream(t, http.StatusOK, upstreamBody)

	models := []config.ModelMapping{{From: "z.ai/glm-5.2", To: "glm-5.2"}}
	px, _ := newTestProxy(t, "both", upstream.URL, "", models)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	px.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	wantBody := `{"id":"chatcmpl-1","model":"z.ai/glm-5.2","choices":[]}`
	if rec.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", rec.Body.String(), wantBody)
	}

	// The rewritten body is longer than the upstream body (the mapped
	// name is longer), so a stale/copied-through Content-Length would
	// be caught here.
	if len(wantBody) == len(upstreamBody) {
		t.Fatalf("test invariant broken: rewritten and original bodies have equal length (%d); this test would not catch a stale Content-Length bug", len(upstreamBody))
	}

	gotCL := rec.Header().Get("Content-Length")
	wantCL := strconv.Itoa(len(wantBody))
	if gotCL != wantCL {
		t.Errorf("Content-Length = %q, want %q (recalculated for rewritten body)", gotCL, wantCL)
	}
}

// --- upstream unreachable (502) -------------------------------------------

func TestUpstreamUnreachable(t *testing.T) {
	// Nothing listens on port 1 (a reserved/privileged port), so the
	// connection is refused quickly.
	px, ctr := newTestProxy(t, "both", "http://127.0.0.1:1", "", nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	px.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "upstream unreachable") {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if got := ctr.Rejected(); got != 1 {
		t.Errorf("Rejected() = %d, want 1", got)
	}
	if !px.HasError() {
		t.Error("HasError() = false, want true")
	}
}

// --- request body size limit -------------------------------------------

// infiniteReader produces an endless stream of filler bytes without
// holding a large buffer in memory; paired with io.LimitReader to
// simulate an oversized request body cheaply.
type infiniteReader struct{}

func (infiniteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestBodySizeLimit(t *testing.T) {
	const maxBodySize = 100 << 20 // must match proxy.go's maxBodySize

	upstream := jsonUpstream(t, http.StatusOK, `{"ok":true}`)
	px, ctr := newTestProxy(t, "both", upstream.URL, "", nil)

	oversized := io.NopCloser(io.LimitReader(infiniteReader{}, maxBodySize+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", oversized)
	rec := httptest.NewRecorder()
	px.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "failed to read request body") {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if got := ctr.Rejected(); got != 1 {
		t.Errorf("Rejected() = %d, want 1", got)
	}
	if !px.HasError() {
		t.Error("HasError() = false, want true")
	}
}

// --- path stripping -----------------------------------------------------

func TestPathStripping(t *testing.T) {
	tests := []struct {
		reqPath      string
		wantUpstream string
	}{
		{"/v1/chat/completions", "/chat/completions"},
		{"/v1", "/"},
		{"/v1/", "/"},
		{"/v1/models", "/models"},
	}

	for _, tt := range tests {
		t.Run(tt.reqPath, func(t *testing.T) {
			var gotPath string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`))
			}))
			t.Cleanup(upstream.Close)

			px, _ := newTestProxy(t, "both", upstream.URL, "", nil)

			req := httptest.NewRequest(http.MethodPost, tt.reqPath, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()
			px.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if gotPath != tt.wantUpstream {
				t.Errorf("upstream path = %q, want %q", gotPath, tt.wantUpstream)
			}
		})
	}
}

// --- hop-by-hop header helpers -------------------------------------------

func TestIsHopHeader(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"Connection", true},
		{"connection", true},
		{"CONNECTION", true},
		{"Keep-Alive", true},
		{"Proxy-Authenticate", true},
		{"Proxy-Authorization", true},
		{"Te", true},
		{"Trailers", true},
		{"Transfer-Encoding", true},
		{"Upgrade", true},
		{"Accept-Encoding", true},
		{"accept-encoding", true},
		{"Content-Type", false},
		{"Authorization", false},
		{"X-Custom-Header", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			if got := isHopHeader(tt.header); got != tt.want {
				t.Errorf("isHopHeader(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestCopyHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Connection", "keep-alive")
	src.Add("X-Multi", "a")
	src.Add("X-Multi", "b")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyHeaders(dst, src)

	if _, ok := dst["Connection"]; ok {
		t.Error("hop header Connection was copied, should have been dropped")
	}
	if got := dst.Values("X-Multi"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("X-Multi = %v, want [a b] (multi-value header not preserved via Add)", got)
	}
	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// --- body rewriting helpers -----------------------------------------------

func TestRewriteRequestBody(t *testing.T) {
	fwd := map[string]string{"z.ai/glm-5.2": "glm-5.2"}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "model field, no space after colon",
			in:   `{"model":"z.ai/glm-5.2","stream":true}`,
			want: `{"model":"glm-5.2","stream":true}`,
		},
		{
			name: "model field, space after colon",
			in:   `{"model": "z.ai/glm-5.2","stream":true}`,
			want: `{"model": "glm-5.2","stream":true}`,
		},
		{
			name: "unmapped model passes through unchanged",
			in:   `{"model":"gpt-4","stream":true}`,
			want: `{"model":"gpt-4","stream":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(rewriteRequestBody([]byte(tt.in), fwd))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteResponseBody(t *testing.T) {
	rev := map[string]string{"glm-5.2": "z.ai/glm-5.2"}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "model and id both rewritten, no space",
			in:   `{"id":"glm-5.2","model":"glm-5.2"}`,
			want: `{"id":"z.ai/glm-5.2","model":"z.ai/glm-5.2"}`,
		},
		{
			name: "model field, space after colon",
			in:   `{"model": "glm-5.2"}`,
			want: `{"model": "z.ai/glm-5.2"}`,
		},
		{
			name: "unmapped model passes through unchanged",
			in:   `{"model":"gpt-4"}`,
			want: `{"model":"gpt-4"}`,
		},
		{
			name: "id that isn't a mapped model name is left alone",
			in:   `{"id":"chatcmpl-abc123","model":"glm-5.2"}`,
			want: `{"id":"chatcmpl-abc123","model":"z.ai/glm-5.2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(rewriteResponseBody([]byte(tt.in), rev))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteResponseLineDoneMarkerUntouched(t *testing.T) {
	rev := map[string]string{"glm-5.2": "z.ai/glm-5.2"}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"done marker left untouched even though it starts with data:", `data: [DONE]`, `data: [DONE]`},
		{"non-data line left untouched", `: comment`, `: comment`},
		{"blank line left untouched", ``, ``},
		{"data line rewritten", `data: {"model":"glm-5.2"}`, `data: {"model":"z.ai/glm-5.2"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(rewriteResponseLine([]byte(tt.in), rev))
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
