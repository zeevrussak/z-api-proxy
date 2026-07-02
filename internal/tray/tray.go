package tray

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows/registry"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/counter"
	"z-api-proxy/internal/proxy"
	"z-api-proxy/internal/tunnel"
	"z-api-proxy/internal/updater"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	mbIconError   uintptr = 0x00000010
	mbIconWarning uintptr = 0x00000030
	mbIconInfo    uintptr = 0x00000040
)

const (
	appName    = "Z-API-Proxy"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func messageBox(text, title string, flags uintptr) {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
}

// --- Tunnel preference ---

func tunnelPrefPath() string {
	return filepath.Join(config.AppConfigDir(), "tunnel.pref")
}

func loadTunnelPref() bool {
	data, err := os.ReadFile(tunnelPrefPath())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "1"
}

func saveTunnelPref(on bool) {
	val := "0"
	if on {
		val = "1"
	}
	if err := os.WriteFile(tunnelPrefPath(), []byte(val), 0644); err != nil {
		log.Printf("tunnel pref: cannot persist: %v", err)
	}
}

// --- Windows autostart (Run registry key) ---

func startupPrefPath() string {
	return filepath.Join(config.AppConfigDir(), "autostart.pref")
}

// loadStartupPref reads the persisted autostart preference.
// Default (no file yet) is true.
func loadStartupPref() bool {
	data, err := os.ReadFile(startupPrefPath())
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(data)) != "0"
}

func saveStartupPref(on bool) {
	val := "1"
	if !on {
		val = "0"
	}
	if err := os.WriteFile(startupPrefPath(), []byte(val), 0644); err != nil {
		log.Printf("autostart: cannot persist preference: %v", err)
	}
}

// autoStartEnabled reports whether the Windows Run registry value exists.
func autoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	return err == nil
}

// setAutoStart adds or removes the Windows Run registry entry so the app
// launches at login.
func setAutoStart(on bool) {
	if on {
		exePath, err := os.Executable()
		if err != nil {
			log.Printf("autostart: cannot resolve exe path: %v", err)
			return
		}
		k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			log.Printf("autostart: cannot open run key: %v", err)
			return
		}
		defer k.Close()
		if err := k.SetStringValue(appName, exePath); err != nil {
			log.Printf("autostart: cannot set run value: %v", err)
		}
		return
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.DeleteValue(appName)
}

type trayApp struct {
	iconNormal []byte
	iconError  []byte
	manager    *config.Manager
	counter    *counter.Counter
	proxy      *proxy.Proxy
	configPath string
	tunnel     *tunnel.Manager
	version    string
}

func Run(iconNormal, iconError []byte, manager *config.Manager, ctr *counter.Counter, px *proxy.Proxy, configPath string, version string) {
	cfg := manager.Get()
	app := &trayApp{
		iconNormal: iconNormal,
		iconError:  iconError,
		manager:    manager,
		counter:    ctr,
		proxy:      px,
		configPath: configPath,
		tunnel:     tunnel.New(cfg.Server.Listen),
		version:    version,
	}
	systray.Run(app.onReady, app.onExit)
}

func (t *trayApp) onReady() {
	systray.SetIcon(t.iconNormal)
	systray.SetTitle("Z-API Proxy")
	systray.SetTooltip("Z-API Proxy")

	startupPref := loadStartupPref()
	setAutoStart(startupPref)

	mStatus := systray.AddMenuItem("Z-API Proxy", "Running")
	mStatus.Disable()

	systray.AddSeparator()

	mConfig := systray.AddMenuItem("Configure...", "Open config.toml in Notepad")
	mTest := systray.AddMenuItem("Test Connection", "Test upstream reachability")
	mCopyURL := systray.AddMenuItem("Copy Base URL", "Copy the proxy base URL for Cursor")
	mTunnel := systray.AddMenuItem("Start Public Tunnel", "Expose proxy on a public URL for Cursor")
	mStartup := systray.AddMenuItemCheckbox("Start with Windows", "Launch Z-API Proxy when Windows starts", startupPref)

	systray.AddSeparator()

	mUpdate := systray.AddMenuItem("Update", "Check for updates")
	mUpdate.Hide()

	mContact := systray.AddMenuItem("Contact Developer", "Send an email to the developer")

	systray.AddSeparator()

	mExit := systray.AddMenuItem("Exit", "Quit Z-API Proxy")

	go t.updateTooltip()
	go t.updateIcon()
	go t.handleMenu(mConfig, mTest, mCopyURL, mTunnel, mStartup, mUpdate, mContact, mExit)
	go t.checkForUpdates(mUpdate)

	// Auto-start tunnel if previously enabled
	if loadTunnelPref() {
		go t.autoStartTunnel(mTunnel, mCopyURL)
	}
}

func (t *trayApp) onExit() {}

func (t *trayApp) updateTooltip() {
	for {
		time.Sleep(time.Second)
		cfg := t.manager.Get()
		h, r := t.counter.Handled(), t.counter.Rejected()
		status := "OK"
		if t.proxy.HasError() {
			status = "ERROR"
		}
		tip := fmt.Sprintf("Z-API Proxy — %s [%s]\nHandled: %d | Rejected: %d",
			cfg.Server.Listen, status, h, r)
		systray.SetTooltip(tip)
	}
}

func (t *trayApp) updateIcon() {
	wasError := false
	for {
		time.Sleep(500 * time.Millisecond)
		isErr := t.proxy.HasError()
		if isErr != wasError {
			if isErr {
				systray.SetIcon(t.iconError)
			} else {
				systray.SetIcon(t.iconNormal)
			}
			wasError = isErr
		}
	}
}

func (t *trayApp) handleMenu(mConfig, mTest, mCopyURL, mTunnel, mStartup, mUpdate, mContact, mExit *systray.MenuItem) {
	for {
		select {
		case <-mConfig.ClickedCh:
			if err := exec.Command("notepad.exe", t.configPath).Start(); err != nil {
				log.Printf("failed to open notepad: %v", err)
			}

		case <-mTest.ClickedCh:
			go t.testConnection()

		case <-mCopyURL.ClickedCh:
			go t.copyBaseURL(mCopyURL)

		case <-mTunnel.ClickedCh:
			go t.toggleTunnel(mTunnel, mCopyURL)

		case <-mStartup.ClickedCh:
			nowOn := !mStartup.Checked()
			if nowOn {
				mStartup.Check()
			} else {
				mStartup.Uncheck()
			}
			saveStartupPref(nowOn)
			setAutoStart(nowOn)

		case <-mUpdate.ClickedCh:
			go t.installUpdate(mUpdate)

		case <-mContact.ClickedCh:
			if err := exec.Command("rundll32", "url.dll,FileProtocolHandler",
				"mailto:zaiproxy.contact@20032014.xyz?subject=Z-API%20Proxy%20Feedback").Start(); err != nil {
				log.Printf("failed to open mail client: %v", err)
			}

		case <-mExit.ClickedCh:
			t.tunnel.Stop()
			systray.Quit()
			return
		}
	}
}

// toggleTunnel starts or stops the public tunnel. Starting is async because
// it involves downloading cloudflared and waiting for the URL.
func (t *trayApp) toggleTunnel(mTunnel, mCopyURL *systray.MenuItem) {
	if t.tunnel.Running() {
		t.tunnel.Stop()
		saveTunnelPref(false)
		mTunnel.SetTitle("Start Public Tunnel")
		mCopyURL.SetTitle("Copy Base URL")
		return
	}

	if !t.tunnel.IsDownloaded() {
		messageBox("Starting tunnel...\n\nThis will download cloudflared (~50 MB) on first use.\nPlease wait, this may take a minute.", "Z-API Proxy — Tunnel", mbIconInfo)
	}

	mTunnel.SetTitle("Starting tunnel...")
	url, err := t.tunnel.Start()
	if err != nil {
		log.Printf("tunnel error: %v", err)
		messageBox("Failed to start tunnel:\n\n"+err.Error(), "Z-API Proxy — Tunnel", mbIconError)
		mTunnel.SetTitle("Start Public Tunnel")
		return
	}

	saveTunnelPref(true)
	mTunnel.SetTitle("Stop Public Tunnel")
	mCopyURL.SetTitle("Copy Public Tunnel URL")

	messageBox(fmt.Sprintf("Public tunnel is live and verified!\n\n%s/v1\n\nUse this URL in Cursor:\nSettings \u2192 Models \u2192 OpenAI API Base URL", url), "Z-API Proxy — Tunnel", mbIconInfo)
}

// autoStartTunnel is called on startup when the tunnel preference is enabled.
// It starts the tunnel silently (no popup dialog on success).
func (t *trayApp) autoStartTunnel(mTunnel, mCopyURL *systray.MenuItem) {
	url, err := t.tunnel.Start()
	if err != nil {
		log.Printf("tunnel auto-start error: %v", err)
		return
	}
	mTunnel.SetTitle("Stop Public Tunnel")
	mCopyURL.SetTitle("Copy Public Tunnel URL")
	log.Printf("tunnel auto-started: %s", url)
}

func (t *trayApp) testConnection() {
	cfg := t.manager.Get()
	url := strings.TrimSuffix(cfg.Upstream.BaseURL, "/") + "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		messageBox("Failed to build request:\n"+err.Error(), "Z-API Proxy — Test", mbIconError)
		return
	}
	if cfg.Upstream.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Upstream.APIKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		messageBox("Connection failed:\n"+err.Error(), "Z-API Proxy — Test", mbIconError)
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 200:
		messageBox("Connection successful.\nUpstream is reachable and authenticated.", "Z-API Proxy — Test", mbIconInfo)
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		messageBox("Upstream is reachable.\nHTTP 401 — authentication required.\nIf api_key is empty, the proxy passes through the key from Cursor at runtime.", "Z-API Proxy — Test", mbIconInfo)
	default:
		messageBox(fmt.Sprintf("Upstream is reachable.\nHTTP %d", resp.StatusCode), "Z-API Proxy — Test", mbIconWarning)
	}
}

// copyBaseURL writes the active base URL to the Windows clipboard.
// When the tunnel is running, copies the public tunnel URL; otherwise
// copies the local proxy URL. Both include the /v1 suffix.
func (t *trayApp) copyBaseURL(mCopyURL *systray.MenuItem) {
	var baseURL string
	if tunnelURL := t.tunnel.URL(); tunnelURL != "" {
		baseURL = tunnelURL + "/v1"
	} else {
		cfg := t.manager.Get()
		baseURL = fmt.Sprintf("http://%s/v1", cfg.Server.Listen)
	}

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Set-Clipboard -Value '%s'", baseURL))
	if err := cmd.Run(); err != nil {
		log.Printf("clipboard error: %v", err)
		messageBox("Failed to copy to clipboard:\n"+err.Error(), "Z-API Proxy", mbIconError)
		return
	}

	messageBox(fmt.Sprintf("Copied to clipboard:\n\n%s\n\nPaste this into Cursor:\nSettings → Models → OpenAI API Base URL", baseURL), "Z-API Proxy — Copy", mbIconInfo)
}

// checkForUpdates queries GitHub for the latest release on startup.
// If a newer version is found, the menu item is revealed with the version.
func (t *trayApp) checkForUpdates(mUpdate *systray.MenuItem) {
	if t.version == "dev" {
		return
	}

	rel, err := updater.FetchLatest()
	if err != nil {
		log.Printf("updater: %v", err)
		return
	}
	if !updater.IsNewer(t.version, rel.TagName) {
		return
	}

	tagDisplay := strings.TrimPrefix(rel.TagName, "v")
	mUpdate.SetTitle(fmt.Sprintf("Update Available! v%s (click to install)", tagDisplay))
	mUpdate.Show()
	log.Printf("updater: update available — %s (current: %s)", rel.TagName, t.version)
}

// installUpdate downloads and launches the MSI installer for the current
// architecture. The app exits after launching so the installer can replace
// files.
func (t *trayApp) installUpdate(mUpdate *systray.MenuItem) {
	mUpdate.SetTitle("Downloading update...")

	rel, err := updater.FetchLatest()
	if err != nil {
		mUpdate.SetTitle("Update failed — see log")
		log.Printf("updater: %v", err)
		return
	}

	if err := rel.DownloadAndInstall(); err != nil {
		mUpdate.SetTitle("Update failed — see log")
		messageBox("Update download failed:\n"+err.Error(), "Z-API Proxy — Update", mbIconError)
		return
	}

	messageBox("Update downloaded. The installer will now launch.\nThe app will exit and restart after installation.", "Z-API Proxy — Update", mbIconInfo)
	t.tunnel.Stop()
	systray.Quit()
}
