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
	"context"
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
