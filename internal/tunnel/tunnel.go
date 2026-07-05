// Package tunnel manages an optional Cloudflare Quick Tunnel (cloudflared)
// that exposes the local proxy on a public HTTPS URL.
//
// This is needed because Cursor's cloud servers block requests to private
// network addresses (127.0.0.1). The tunnel gives the proxy a public
// trycloudflare.com URL that Cursor's servers can reach.
//
// The cloudflared binary is downloaded on first use and cached in the
// app's config directory. No Cloudflare account or configuration is needed.
package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	cloudflaredDownloadBase = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-"
	cloudflaredExeName      = "cloudflared.exe"
)

// tunnelURLPattern matches the trycloudflare.com URL printed by cloudflared.
var tunnelURLPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Manager controls the cloudflared subprocess lifecycle.
// It is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	url      string
	listen   string
	cacheDir string
	mode     string // "quick" or "named"
	token    string // Cloudflare tunnel token (named mode)
	hostname string // stable hostname (named mode)
}

// New creates a Manager that will tunnel traffic to the given local
// listen address (e.g. "127.0.0.1:8787"). For named tunnels, pass
// mode="named", a Cloudflare token, and the stable hostname.
func New(listen, mode, token, hostname string) *Manager {
	if mode == "" {
		mode = "quick"
	}
	if mode == "named" && hostname != "" {
		if !strings.HasPrefix(hostname, "https://") {
			hostname = "https://" + hostname
		}
	}
	return &Manager{
		listen:   listen,
		cacheDir: cacheDir(),
		mode:     mode,
		token:    token,
		hostname: hostname,
	}
}

// cacheDir returns the directory where cloudflared.exe is stored.
func cacheDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, ".config")
	}
	dir := filepath.Join(appData, "Z-API-Proxy")
	os.MkdirAll(dir, 0755)
	return dir
}

// cloudflaredPath returns the cached path to the cloudflared binary.
func (m *Manager) cloudflaredPath() string {
	return filepath.Join(m.cacheDir, cloudflaredExeName)
}

// IsDownloaded reports whether cloudflared.exe has been cached locally.
func (m *Manager) IsDownloaded() bool {
	_, err := os.Stat(m.cloudflaredPath())
	return err == nil
}

// ensureDownloaded downloads cloudflared.exe if not already cached.
func (m *Manager) ensureDownloaded() error {
	if m.IsDownloaded() {
		return nil
	}

	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	url := cloudflaredDownloadBase + arch + ".exe"

	log.Printf("tunnel: downloading cloudflared from %s", url)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	// Cap download at 200MB to prevent memory exhaustion from a
	// compromised or misconfigured CDN response.
	const maxDownloadSize = 200 << 20 // 200 MB
	limitedBody := io.LimitReader(resp.Body, maxDownloadSize)

	exePath := m.cloudflaredPath()
	out, err := os.Create(exePath)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, limitedBody); err != nil {
		os.Remove(exePath)
		return fmt.Errorf("download write failed: %w", err)
	}

	log.Printf("tunnel: cloudflared saved to %s", exePath)
	return nil
}

// Running reports whether the tunnel is currently active.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil
}

// URL returns the current public tunnel URL, or empty string if not
// yet established.
func (m *Manager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url
}

// Start downloads cloudflared (if needed) and launches the tunnel.
// For quick tunnels it scans cloudflared output for the ephemeral URL.
// For named tunnels the URL is known upfront from config.
// In both cases a health check verifies reachability before returning.
func (m *Manager) Start() (string, error) {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("tunnel is already running")
	}
	m.mu.Unlock()

	if err := m.ensureDownloaded(); err != nil {
		return "", fmt.Errorf("cannot download cloudflared: %w", err)
	}

	// Named tunnel: URL is known from config, just launch and verify.
	if m.mode == "named" && m.token != "" {
		return m.startNamed()
	}
	return m.startQuick()
}

// startNamed launches a Cloudflare Named Tunnel using a token from the
// Zero Trust dashboard. The hostname is stable and known from config.
func (m *Manager) startNamed() (string, error) {
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, m.cloudflaredPath(),
		"tunnel", "--no-autoupdate", "run")
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+m.token)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("cannot start cloudflared: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.cancel = cancel
	m.url = m.hostname
	m.mu.Unlock()

	log.Printf("tunnel: named tunnel started (PID %d), verifying %s...", cmd.Process.Pid, m.hostname)

	go func() {
		err := cmd.Wait()
		log.Printf("tunnel: cloudflared exited: %v", err)
		m.mu.Lock()
		m.cmd = nil
		m.cancel = nil
		m.url = ""
		m.mu.Unlock()
	}()

	if err := m.healthCheck(m.hostname); err != nil {
		m.Stop()
		return "", fmt.Errorf("named tunnel not reachable: %w", err)
	}

	log.Printf("tunnel: named tunnel active = %s", m.hostname)
	return m.hostname, nil
}

// startQuick launches a Cloudflare Quick Tunnel with an ephemeral URL.
// Scans cloudflared output for the trycloudflare.com URL.
func (m *Manager) startQuick() (string, error) {
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, m.cloudflaredPath(),
		"tunnel", "--no-autoupdate", "--url", "http://"+m.listen)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("stdout pipe error: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("stderr pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("cannot start cloudflared: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.cancel = cancel
	m.url = ""
	m.mu.Unlock()

	log.Printf("tunnel: cloudflared started (PID %d), waiting for URL...", cmd.Process.Pid)

	urlCh := make(chan string, 2)
	exitCh := make(chan error, 1)

	scan := func(name string, reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("tunnel[%s]: %s", name, line)
			if match := tunnelURLPattern.FindString(line); match != "" {
				select {
				case urlCh <- match:
				default:
				}
			}
		}
	}

	go scan("stdout", stdout)
	go scan("stderr", stderr)

	go func() {
		err := cmd.Wait()
		log.Printf("tunnel: cloudflared exited: %v", err)
		m.mu.Lock()
		m.cmd = nil
		m.cancel = nil
		m.url = ""
		m.mu.Unlock()
		exitCh <- err
	}()

	var tunnelURL string
	select {
	case url := <-urlCh:
		tunnelURL = url
	case <-exitCh:
		return "", fmt.Errorf("cloudflared exited before establishing tunnel — check proxy.log for details")
	case <-time.After(60 * time.Second):
		m.Stop()
		return "", fmt.Errorf("timed out after 60s waiting for tunnel URL — cloudflared may be blocked by a firewall")
	}

	log.Printf("tunnel: verifying reachability of %s", tunnelURL)
	if err := m.healthCheck(tunnelURL); err != nil {
		m.Stop()
		return "", fmt.Errorf("tunnel URL obtained but not reachable: %w", err)
	}

	m.mu.Lock()
	m.url = tunnelURL
	m.mu.Unlock()
	log.Printf("tunnel: public URL verified and active = %s", tunnelURL)
	return tunnelURL, nil
}

// healthCheck pings the tunnel URL to confirm it is serving traffic.
// It retries for up to 30 seconds since the tunnel may need a moment
// to become fully active after the URL is printed.
func (m *Manager) healthCheck(url string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 15; i++ {
		resp, err := client.Get(url + "/v1/models")
		if err == nil {
			resp.Body.Close()
			// Any non-5xx response means the tunnel is working (the proxy
			// is receiving requests through the tunnel).
			if resp.StatusCode < 500 {
				log.Printf("tunnel: health check passed (HTTP %d)", resp.StatusCode)
				return nil
			}
			log.Printf("tunnel: health check attempt %d got HTTP %d", i+1, resp.StatusCode)
		} else {
			log.Printf("tunnel: health check attempt %d failed: %v", i+1, err)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("tunnel did not become reachable after 30 seconds")
}

// Stop terminates the cloudflared subprocess if running.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
	}
	m.cmd = nil
	m.cancel = nil
	m.url = ""
	log.Printf("tunnel: stopped")
}

// TunnelCreationResult holds the result of creating a named tunnel via API.
type TunnelCreationResult struct {
	TunnelID string
	Token    string // The connector token for cloudflared
	Hostname string // The public hostname that routes to the tunnel
}

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// CreateNamedTunnelViaAPI creates a Cloudflare Named Tunnel via the REST API,
// configures ingress rules, and sets up DNS — all automatically.
//
// The API token needs these permissions:
//   - Account → Cloudflare Tunnel → Edit
//   - Zone → DNS → Edit
//
// Parameters:
//   - accountID: Cloudflare account ID
//   - apiToken: Cloudflare API token with the permissions above
//   - hostname: desired public hostname (e.g. "proxy.yourdomain.com")
//   - listenAddr: local address the proxy listens on (e.g. "127.0.0.1:8787")
func CreateNamedTunnelViaAPI(accountID, apiToken, hostname, listenAddr string) (*TunnelCreationResult, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	// 1. Generate a tunnel secret (32 bytes, base64).
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("cannot generate tunnel secret: %w", err)
	}
	tunnelSecret := base64.StdEncoding.EncodeToString(secretBytes)

	// 2. Extract domain from hostname for zone lookup.
	// e.g. "proxy.example.com" → "example.com"
	domainParts := strings.SplitN(hostname, ".", 2)
	if len(domainParts) < 2 {
		return nil, fmt.Errorf("invalid hostname %q — must be like proxy.yourdomain.com", hostname)
	}
	domain := domainParts[1]
	// Strip https:// prefix if present.
	hostname = strings.TrimPrefix(hostname, "https://")
	hostname = strings.TrimPrefix(hostname, "http://")

	// 3. Create the tunnel.
	tunnelName := "z-api-proxy"
	createBody, _ := json.Marshal(map[string]string{
		"name":          tunnelName,
		"tunnel_secret": tunnelSecret,
		"config_src":    "cloudflare",
	})

	createURL := fmt.Sprintf("%s/accounts/%s/cfd_tunnel", cfAPIBase, accountID)
	req, _ := http.NewRequest("POST", createURL, bytes.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	log.Printf("tunnel-api: creating named tunnel '%s'...", tunnelName)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create tunnel API call failed: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create tunnel returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var createResult struct {
		Result struct {
			ID          string `json:"id"`
			ConnectToken string `json:"connect_token"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &createResult); err != nil {
		return nil, fmt.Errorf("cannot parse tunnel creation response: %w", err)
	}
	if createResult.Result.ID == "" {
		return nil, fmt.Errorf("tunnel created but no ID returned: %s", string(respBody))
	}
	tunnelID := createResult.Result.ID
	token := createResult.Result.ConnectToken

	if token == "" {
		// Some API versions return the token differently — try the connector token endpoint.
		tokenURL := fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s/token", cfAPIBase, accountID, tunnelID)
		tokReq, _ := http.NewRequest("GET", tokenURL, nil)
		tokReq.Header.Set("Authorization", "Bearer "+apiToken)
		tokResp, err := client.Do(tokReq)
		if err == nil {
			tokBody, _ := io.ReadAll(tokResp.Body)
			tokResp.Body.Close()
			var tokResult struct {
				Result string `json:"result"`
			}
			json.Unmarshal(tokBody, &tokResult)
			token = tokResult.Result
		}
	}

	if token == "" {
		return nil, fmt.Errorf("tunnel created (ID: %s) but could not obtain connector token", tunnelID)
	}

	log.Printf("tunnel-api: tunnel created, ID=%s", tunnelID)

	// 4. Configure ingress rules (hostname → localhost:8787).
	ingressBody, _ := json.Marshal(map[string]interface{}{
		"config": map[string]interface{}{
			"ingress": []map[string]interface{}{
				{
					"hostname": hostname,
					"service":  "http://" + listenAddr,
				},
				{
					"service": "http_status:404",
				},
			},
		},
	})

	ingressURL := fmt.Sprintf("%s/accounts/%s/cfd_tunnel/%s/configurations", cfAPIBase, accountID, tunnelID)
	ingReq, _ := http.NewRequest("PUT", ingressURL, bytes.NewReader(ingressBody))
	ingReq.Header.Set("Authorization", "Bearer "+apiToken)
	ingReq.Header.Set("Content-Type", "application/json")

	log.Printf("tunnel-api: configuring ingress %s → http://%s...", hostname, listenAddr)
	ingResp, err := client.Do(ingReq)
	if err != nil {
		return nil, fmt.Errorf("configure ingress failed: %w", err)
	}
	ingResp.Body.Close()

	if ingResp.StatusCode != http.StatusOK {
		ingRespBody, _ := io.ReadAll(ingResp.Body)
		return nil, fmt.Errorf("configure ingress returned HTTP %d: %s", ingResp.StatusCode, string(ingRespBody))
	}

	log.Printf("tunnel-api: ingress configured")

	// 5. Look up the zone ID for the domain.
	zoneURL := fmt.Sprintf("%s/zones?name=%s", cfAPIBase, domain)
	zoneReq, _ := http.NewRequest("GET", zoneURL, nil)
	zoneReq.Header.Set("Authorization", "Bearer "+apiToken)
	zoneResp, err := client.Do(zoneReq)
	if err != nil {
		return nil, fmt.Errorf("zone lookup failed: %w", err)
	}
	zoneBody, _ := io.ReadAll(zoneResp.Body)
	zoneResp.Body.Close()

	var zoneResult struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	if err := json.Unmarshal(zoneBody, &zoneResult); err != nil || len(zoneResult.Result) == 0 {
		return nil, fmt.Errorf("could not find zone for domain %q — ensure the domain is added to your Cloudflare account", domain)
	}
	zoneID := zoneResult.Result[0].ID

	// 6. Create or update DNS CNAME record pointing to the tunnel.
	cnameTarget := fmt.Sprintf("%s.cfargotunnel.com", tunnelID)

	// Check if a record already exists.
	dnsURL := fmt.Sprintf("%s/zones/%s/dns_records?name=%s", cfAPIBase, zoneID, hostname)
	dnsReq, _ := http.NewRequest("GET", dnsURL, nil)
	dnsReq.Header.Set("Authorization", "Bearer "+apiToken)
	dnsResp, err := client.Do(dnsReq)
	if err == nil {
		dnsRespBody, _ := io.ReadAll(dnsResp.Body)
		dnsResp.Body.Close()
		var existingDNS struct {
			Result []struct {
				ID string `json:"id"`
			} `json:"result"`
		}
		json.Unmarshal(dnsRespBody, &existingDNS)
		if len(existingDNS.Result) > 0 {
			// Delete existing record, we'll recreate it.
			for _, rec := range existingDNS.Result {
				delURL := fmt.Sprintf("%s/zones/%s/dns_records/%s", cfAPIBase, zoneID, rec.ID)
				delReq, _ := http.NewRequest("DELETE", delURL, nil)
				delReq.Header.Set("Authorization", "Bearer "+apiToken)
				delResp, err := client.Do(delReq)
				if err == nil {
					delResp.Body.Close()
				}
			}
			log.Printf("tunnel-api: removed existing DNS records for %s", hostname)
		}
	}

	dnsCreateBody, _ := json.Marshal(map[string]interface{}{
		"type":    "CNAME",
		"name":    hostname,
		"content": cnameTarget,
		"proxied": true,
	})
	dnsCreateURL := fmt.Sprintf("%s/zones/%s/dns_records", cfAPIBase, zoneID)
	dnsCreateReq, _ := http.NewRequest("POST", dnsCreateURL, bytes.NewReader(dnsCreateBody))
	dnsCreateReq.Header.Set("Authorization", "Bearer "+apiToken)
	dnsCreateReq.Header.Set("Content-Type", "application/json")

	log.Printf("tunnel-api: creating DNS CNAME %s → %s...", hostname, cnameTarget)
	dnsCreateResp, err := client.Do(dnsCreateReq)
	if err != nil {
		return nil, fmt.Errorf("DNS record creation failed: %w", err)
	}
	dnsCreateRespBody, _ := io.ReadAll(dnsCreateResp.Body)
	dnsCreateResp.Body.Close()

	if dnsCreateResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DNS creation returned HTTP %d: %s", dnsCreateResp.StatusCode, string(dnsCreateRespBody))
	}

	log.Printf("tunnel-api: DNS record created — %s → %s", hostname, cnameTarget)

	return &TunnelCreationResult{
		TunnelID: tunnelID,
		Token:    token,
		Hostname: "https://" + hostname,
	}, nil
}
