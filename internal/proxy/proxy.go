package proxy

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/counter"
)

type Proxy struct {
	manager *config.Manager
	counter *counter.Counter
	client  *http.Client
	hasErr  atomic.Bool
}

func New(manager *config.Manager, ctr *counter.Counter) *Proxy {
	return &Proxy{
		manager: manager,
		counter: ctr,
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (p *Proxy) HasError() bool { return p.hasErr.Load() }

var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
	"Accept-Encoding",
}

func isHopHeader(h string) bool {
	for _, hh := range hopHeaders {
		if strings.EqualFold(h, hh) {
			return true
		}
	}
	return false
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := p.manager.Get()

	// Security: verify the caller's API key matches the configured key.
	if cfg.Security.VerifyKey && cfg.Upstream.APIKey != "" {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+cfg.Upstream.APIKey {
			p.counter.IncRejected()
			http.Error(w, "unauthorized: API key mismatch", http.StatusUnauthorized)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		p.counter.IncRejected()
		p.hasErr.Store(true)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	body = rewriteRequestBody(body, cfg.ForwardMap())

	upstreamPath := strings.TrimPrefix(r.URL.Path, "/v1")
	if upstreamPath == "" || upstreamPath[0] != '/' {
		upstreamPath = "/" + upstreamPath
	}
	upstreamURL := strings.TrimSuffix(cfg.Upstream.BaseURL, "/") + upstreamPath
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		p.counter.IncRejected()
		p.hasErr.Store(true)
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}

	copyHeaders(upReq.Header, r.Header)
	upReq.ContentLength = int64(len(body))

	if cfg.Upstream.APIKey != "" {
		upReq.Header.Set("Authorization", "Bearer "+cfg.Upstream.APIKey)
	}

	log.Printf("%s %s -> %s", r.Method, r.URL.Path, upstreamURL)

	resp, err := p.client.Do(upReq)
	if err != nil {
		p.counter.IncRejected()
		p.hasErr.Store(true)
		log.Printf("upstream error: %v", err)
		http.Error(w, "upstream unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	p.hasErr.Store(false)
	p.counter.IncHandled()

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		p.handleSSE(w, resp, cfg.ReverseMap())
	} else {
		p.handleRegular(w, resp, cfg.ReverseMap())
	}
}

func (p *Proxy) handleSSE(w http.ResponseWriter, resp *http.Response, reverseMap map[string]string) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		line = rewriteResponseLine(line, reverseMap)
		w.Write(line)
		w.Write([]byte("\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (p *Proxy) handleRegular(w http.ResponseWriter, resp *http.Response, reverseMap map[string]string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		return
	}

	body = rewriteResponseBody(body, reverseMap)

	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func rewriteRequestBody(body []byte, fwd map[string]string) []byte {
	for from, to := range fwd {
		body = bytes.ReplaceAll(body,
			[]byte(`"model":"`+from+`"`),
			[]byte(`"model":"`+to+`"`))
		body = bytes.ReplaceAll(body,
			[]byte(`"model": "`+from+`"`),
			[]byte(`"model": "`+to+`"`))
	}
	return body
}

func rewriteResponseLine(line []byte, rev map[string]string) []byte {
	if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("data:")) {
		return line
	}
	if bytes.Contains(line, []byte("[DONE]")) {
		return line
	}
	return rewriteModelFields(line, rev)
}

func rewriteResponseBody(body []byte, rev map[string]string) []byte {
	return rewriteModelFields(body, rev)
}

func rewriteModelFields(data []byte, rev map[string]string) []byte {
	for from, to := range rev {
		for _, field := range []string{`"model"`, `"id"`} {
			for _, sep := range []string{`:`, `: `} {
				old := field + sep + `"` + from + `"`
				new := field + sep + `"` + to + `"`
				data = bytes.ReplaceAll(data, []byte(old), []byte(new))
			}
		}
	}
	return data
}
