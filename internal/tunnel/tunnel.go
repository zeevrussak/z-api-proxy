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
	"sync"
	"time"
)

// cloudflaredDownloadURL is the GitHub release URL for the latest cloudflared.
// We pick the right architecture at runtime.
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
}

// New creates a Manager that will tunnel traffic to the given local
// listen address (e.g. "127.0.0.1:8787").
func New(listen string) *Manager {
	return &Manager{
		listen:   listen,
		cacheDir: cacheDir(),
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

// ensureDownloaded downloads cloudflared.exe if not already cached.
func (m *Manager) ensureDownloaded() error {
	exePath := m.cloudflaredPath()
	if _, err := os.Stat(exePath); err == nil {
		return nil
	}

	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	url := cloudflaredDownloadBase + arch + ".exe"

	log.Printf("tunnel: downloading cloudflared from %s", url)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(exePath)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
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

// Start downloads cloudflared (if needed) and launches a quick tunnel.
// It returns the public URL once cloudflared prints it, or an error.
// The function blocks until the URL is obtained or a timeout occurs.
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

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, m.cloudflaredPath(),
		"tunnel", "--url", "http://"+m.listen)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("pipe error: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("pipe error: %w", err)
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

	// cloudflared prints the tunnel URL to both stdout and stderr.
	// Scan both until we find it.
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)

	scan := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("tunnel: %s", line)
			if match := tunnelURLPattern.FindString(line); match != "" {
				select {
				case urlCh <- match:
				default:
				}
			}
		}
	}

	go scan(stdout)
	go scan(stderr)

	// Wait for process exit in background.
	go func() {
		err := cmd.Wait()
		log.Printf("tunnel: cloudflared exited: %v", err)
		m.mu.Lock()
		m.cmd = nil
		m.cancel = nil
		m.url = ""
		m.mu.Unlock()
		errCh <- err
	}()

	// Wait for URL or exit (whichever comes first), with a 30s timeout.
	select {
	case url := <-urlCh:
		m.mu.Lock()
		m.url = url
		m.mu.Unlock()
		log.Printf("tunnel: public URL = %s", url)
		return url, nil
	case <-errCh:
		return "", fmt.Errorf("cloudflared exited before establishing tunnel")
	case <-time.After(30 * time.Second):
		m.Stop()
		return "", fmt.Errorf("timed out waiting for tunnel URL")
	}
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
