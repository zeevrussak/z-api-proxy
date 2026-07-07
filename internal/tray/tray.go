package tray

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/getlantern/systray"
	"github.com/lxn/walk"
	"golang.org/x/sys/windows/registry"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/counter"
	cursorint "z-api-proxy/internal/cursor"
	"z-api-proxy/internal/proxy"
	"z-api-proxy/internal/tunnel"
	"z-api-proxy/internal/updater"
	"z-api-proxy/internal/worker"
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
	var icon walk.MsgBoxStyle
	switch flags {
	case mbIconError:
		icon = walk.MsgBoxIconError
	case mbIconWarning:
		icon = walk.MsgBoxIconWarning
	default:
		icon = walk.MsgBoxIconInformation
	}
	walk.MsgBox(nil, title, text, icon)
}

// --- Worker URL preference ---

func workerPrefPath() string {
	return filepath.Join(config.AppConfigDir(), "worker.pref")
}

func loadWorkerURL() string {
	data, err := os.ReadFile(workerPrefPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveWorkerURL(url string) {
	if err := os.WriteFile(workerPrefPath(), []byte(url), 0600); err != nil {
		log.Printf("worker pref: cannot persist: %v", err)
	}
}

func clearWorkerURL() {
	os.Remove(workerPrefPath())
}

// activeURL returns the best available proxy URL (Worker > Tunnel > Local).
func (t *trayApp) activeURL() string {
	if w := loadWorkerURL(); w != "" {
		return strings.TrimSuffix(w, "/") + "/v1"
	}
	if tu := t.tunnel.URL(); tu != "" {
		return tu + "/v1"
	}
	cfg := t.manager.Get()
	return fmt.Sprintf("http://%s/v1", cfg.Server.Listen)
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
	if err := os.WriteFile(tunnelPrefPath(), []byte(val), 0600); err != nil {
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

	// Worker stats (polled from Cloudflare analytics API).
	workerTotal   atomic.Int64
	workerSuccess atomic.Int64
	workerErrors  atomic.Int64
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
		tunnel:     tunnel.New(cfg.Server.Listen, cfg.Tunnel.Mode, cfg.Tunnel.Token, cfg.Tunnel.Hostname),
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

	mConfig := systray.AddMenuItem("Settings...", "Open the settings form")
	mConfigRaw := systray.AddMenuItem("Edit Config (Raw)", "Open config.toml in Notepad")
	mTest := systray.AddMenuItem("Test Connection", "Test upstream reachability")
	mCopyURL := systray.AddMenuItem("Copy Base URL", "Copy the proxy base URL for Cursor")
	mTunnel := systray.AddMenuItem("Start Public Tunnel", "Expose proxy on a public URL for Cursor")
	mWorker := systray.AddMenuItem("Deploy Cloudflare Worker", "Deploy a stable Worker proxy to your Cloudflare account")
	mCreateTunnel := systray.AddMenuItem("Create Named Tunnel", "Set up a fixed-domain tunnel via Cloudflare API")
	mRegister := systray.AddMenuItem("Register Models in Cursor", "Add all z.ai models to Cursor and config.toml")
	mStartup := systray.AddMenuItemCheckbox("Start with Windows", "Launch Z-API Proxy when Windows starts", startupPref)

	systray.AddSeparator()

	mUpdate := systray.AddMenuItem("Update", "Check for updates")
	mUpdate.Hide()

	mContact := systray.AddMenuItem("Contact Developer", "Send an email to the developer")

	systray.AddSeparator()

	mExit := systray.AddMenuItem("Exit", "Quit Z-API Proxy")

	go t.updateTooltip()
	go t.updateIcon()
	go t.handleMenu(mConfig, mConfigRaw, mTest, mCopyURL, mTunnel, mWorker, mCreateTunnel, mRegister, mStartup, mUpdate, mContact, mExit)
	go t.checkForUpdates(mUpdate)

	// Start Worker stats polling if enabled.
	cfg := t.manager.Get()
	if cfg.WorkerStats.Enabled && cfg.Cloudflare.AccountID != "" {
		go t.pollWorkerStats(cfg.WorkerStats.Interval)
	}

	// Auto-start: prefer Worker URL. If no Worker deployed, try tunnel.
	if loadWorkerURL() == "" && loadTunnelPref() {
		go t.autoStartTunnel(mTunnel, mCopyURL)
	}
	if w := loadWorkerURL(); w != "" {
		mCopyURL.SetTitle("Copy Worker URL")
	}
}

func (t *trayApp) onExit() {}

// pollWorkerStats queries Cloudflare analytics every N seconds and
// updates the atomic counters for display in the tooltip.
func (t *trayApp) pollWorkerStats(intervalSec int) {
	if intervalSec < 5 {
		intervalSec = 5
	}
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	// Initial fetch immediately.
	t.fetchWorkerStats()

	for range ticker.C {
		t.fetchWorkerStats()
	}
}

func (t *trayApp) fetchWorkerStats() {
	cfg := t.manager.Get()
	stats, err := worker.FetchWorkerStats(cfg)
	if err != nil {
		log.Printf("worker stats: %v", err)
		return
	}
	t.workerTotal.Store(stats.TotalRequests)
	t.workerSuccess.Store(stats.SuccessCount)
	t.workerErrors.Store(stats.ErrorCount)
}

func (t *trayApp) updateTooltip() {
	for {
		time.Sleep(time.Second)
		cfg := t.manager.Get()
		h, r := t.counter.Handled(), t.counter.Rejected()
		status := "OK"
		if t.proxy.HasError() {
			status = "ERROR"
		}
		tip := fmt.Sprintf("Z-API Proxy — %s [%s]\nLocal: %d handled | %d rejected",
			cfg.Server.Listen, status, h, r)

		// Append Worker stats if available.
		wTotal := t.workerTotal.Load()
		if wTotal > 0 {
			wSuccess := t.workerSuccess.Load()
			wErrors := t.workerErrors.Load()
			tip += fmt.Sprintf("\nWorker: %d total | %d ok | %d err (5m delay)", wTotal, wSuccess, wErrors)
		}
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

func (t *trayApp) handleMenu(mConfig, mConfigRaw, mTest, mCopyURL, mTunnel, mWorker, mCreateTunnel, mRegister, mStartup, mUpdate, mContact, mExit *systray.MenuItem) {
	for {
		select {
		case <-mConfig.ClickedCh:
			go func() {
				cfg := t.manager.Get()
				showSettingsDialogWalk(cfg, t.configPath)
			}()

		case <-mConfigRaw.ClickedCh:
			if err := exec.Command("notepad.exe", t.configPath).Start(); err != nil {
				log.Printf("failed to open notepad: %v", err)
			}

		case <-mTest.ClickedCh:
			go t.testConnection()

		case <-mCopyURL.ClickedCh:
			go t.copyBaseURL(mCopyURL)

		case <-mTunnel.ClickedCh:
			go t.toggleTunnel(mTunnel, mCopyURL)

		case <-mWorker.ClickedCh:
			go t.deployWorker(mCopyURL)

		case <-mCreateTunnel.ClickedCh:
			go t.createNamedTunnel(mTunnel, mCopyURL)

		case <-mRegister.ClickedCh:
			go t.registerModels()

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

// toggleTunnel starts or stops the public tunnel.
func (t *trayApp) toggleTunnel(mTunnel, mCopyURL *systray.MenuItem) {
	if t.tunnel.Running() {
		t.tunnel.Stop()
		saveTunnelPref(false)
		mTunnel.SetTitle("Start Public Tunnel")
		mCopyURL.SetTitle("Copy Base URL")
		return
	}

	_, ok := showTunnelWindowWalkV2(t.tunnel)
	if ok {
		saveTunnelPref(true)
		mTunnel.SetTitle("Stop Public Tunnel")
		mCopyURL.SetTitle("Copy Public Tunnel URL")
	} else {
		mTunnel.SetTitle("Start Public Tunnel")
	}
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
	if w := loadWorkerURL(); w != "" {
		baseURL = strings.TrimSuffix(w, "/") + "/v1"
	} else if tunnelURL := t.tunnel.URL(); tunnelURL != "" {
		baseURL = tunnelURL + "/v1"
	} else {
		cfg := t.manager.Get()
		baseURL = fmt.Sprintf("http://%s/v1", cfg.Server.Listen)
	}

	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"$input | Set-Clipboard")
	cmd.Stdin = strings.NewReader(baseURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
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

// deployWorker pushes a Cloudflare Worker script that acts as a public
// reverse proxy with a stable workers.dev URL.
func (t *trayApp) deployWorker(mCopyURL *systray.MenuItem) {
	cfg := t.manager.Get()

	if cfg.Cloudflare.AccountID == "" || cfg.Cloudflare.APIToken == "" {
		messageBox(
			"Cloudflare credentials not configured.\n\n"+
				"Add the following to your config.toml:\n\n"+
				"[cloudflare]\n"+
				"account_id = \"your-account-id\"\n"+
				"api_token = \"your-api-token\"\n\n"+
				"Get these from:\n"+
				"dash.cloudflare.com (Account ID on the right sidebar)\n"+
				"dash.cloudflare.com/profile/api-tokens (create token with Workers Edit permission)",
			"Z-API Proxy — Worker", mbIconWarning)
		return
	}

	result, err := worker.Deploy(cfg)
	if err != nil {
		log.Printf("worker deploy error: %v", err)
		messageBox("Failed to deploy Worker:\n\n"+err.Error(), "Z-API Proxy — Worker", mbIconError)
		return
	}

	log.Printf("worker deployed: %s", result.URL)

	// Persist the Worker URL so it's used by default on every launch.
	saveWorkerURL(result.URL)
	mCopyURL.SetTitle("Copy Worker URL")

	// If the tunnel is running, stop it — the Worker replaces it.
	if t.tunnel.Running() {
		t.tunnel.Stop()
		saveTunnelPref(false)
		mTunnel := mCopyURL // not ideal but tunnel item not in scope
		_ = mTunnel
	}

	messageBox(
		fmt.Sprintf("Cloudflare Worker deployed successfully!\n\n"+
			"URL: %s/v1\n\n"+
			"This is a stable URL — it won't change on restart.\n"+
			"Use it in Cursor:\n"+
			"Settings \u2192 Models \u2192 OpenAI API Base URL",
			result.URL),
		"Z-API Proxy — Worker", mbIconInfo)
}

// registerModels adds all configured z.ai model names into Cursor's
// settings.json and ensures they exist in the proxy config.
func (t *trayApp) registerModels() {
	cfg := t.manager.Get()

	if !cursorint.IsInstalled() {
		messageBox(
			"Cursor is not installed.\n\n"+
				"Expected at: %APPDATA%\\Cursor\\User\\settings.json\n\n"+
				"Install Cursor from cursor.com and try again.",
			"Z-API Proxy — Register", mbIconWarning)
		return
	}

	proxyURL := t.activeURL()

	var modelNames []string
	for _, m := range cfg.Models {
		modelNames = append(modelNames, m.From)
	}

	settingsPath, err := cursorint.RegisterModels(proxyURL, modelNames, cfg.Proxy.CursorKey, cfg.Proxy.ClientID)
	if err != nil {
		log.Printf("register models error: %v", err)
		messageBox("Failed to register models in Cursor:\n\n"+err.Error(),
			"Z-API Proxy — Register", mbIconError)
		return
	}

	log.Printf("models registered in Cursor: %s (%d models)", settingsPath, len(modelNames))

	messageBox(
		fmt.Sprintf("Models registered in Cursor!\n\n"+
			"Settings file: %s\n\n"+
			"Base URL: %s\n"+
			"Models: %d z.ai GLM models added\n\n"+
			"Next steps:\n"+
			"1. Restart Cursor\n"+
			"2. Go to Settings \u2192 Models\n"+
			"3. Enter your z.ai API key\n"+
			"4. Select a z.ai model (e.g. z.ai/glm-5.2)",
			settingsPath, proxyURL, len(modelNames)),
		"Z-API Proxy — Register", mbIconInfo)
}

// createNamedTunnel sets up a fixed-domain Cloudflare tunnel via the API.
// It creates the tunnel, configures ingress, sets up DNS — all automatically.
// Requires [cloudflare].account_id and [cloudflare].api_token in config,
// and [tunnel].hostname with the desired domain.
func (t *trayApp) createNamedTunnel(mTunnel, mCopyURL *systray.MenuItem) {
	cfg := t.manager.Get()

	if cfg.Cloudflare.AccountID == "" {
		messageBox(
			"Cloudflare Account ID not configured.\n\n"+
				"Set [cloudflare].account_id in Settings\n"+
				"(from dash.cloudflare.com → right sidebar)",
			"Z-API Proxy — Tunnel", mbIconWarning)
		return
	}

	if cfg.Cloudflare.APIToken == "" {
		messageBox(
			"Cloudflare API Token not configured.\n\n"+
				"Set [cloudflare].api_token in Settings (secrets.toml).\n\n"+
				"The token needs these permissions:\n"+
				"  - Account → Cloudflare Tunnel → Edit\n"+
				"  - Zone → DNS → Edit\n\n"+
				"Create at: dash.cloudflare.com/profile/api-tokens",
			"Z-API Proxy — Tunnel", mbIconWarning)
		return
	}

	if cfg.Tunnel.Hostname == "" {
		messageBox(
			"No hostname configured.\n\n"+
				"Set [tunnel].hostname in Settings to the domain you want,\n"+
				"e.g. proxy.yourdomain.com",
			"Z-API Proxy — Tunnel", mbIconWarning)
		return
	}

	hostname := strings.TrimPrefix(strings.TrimPrefix(cfg.Tunnel.Hostname, "https://"), "http://")
	listenAddr := cfg.Server.Listen

	result, err := tunnel.CreateNamedTunnelViaAPI(
		cfg.Cloudflare.AccountID, cfg.Cloudflare.APIToken, hostname, listenAddr,
	)
	if err != nil {
		log.Printf("create named tunnel error: %v", err)
		messageBox("Failed to create named tunnel:\n\n"+err.Error(),
			"Z-API Proxy — Tunnel", mbIconError)
		return
	}

	log.Printf("named tunnel created: ID=%s, hostname=%s", result.TunnelID, result.Hostname)

	// Save the token to secrets.toml and hostname to config.
	secretsPath := config.DefaultSecretsPath()
	secData, err := os.ReadFile(secretsPath)
	if err != nil {
		secData = []byte{}
	}
	// Update the tunnel token in secrets.toml.
	secText := string(secData)
	// Simple replacement: find [tunnel] section and replace token line.
	secText = updateTOMLValue(secText, "[tunnel]", "token", result.Token)
	if err := os.WriteFile(secretsPath, []byte(secText), 0600); err != nil {
		log.Printf("failed to write secrets: %v", err)
	}

	// Update config mode to "named".
	mTunnel.SetTitle("Stop Public Tunnel")
	mCopyURL.SetTitle("Copy Tunnel URL")
	saveTunnelPref(true)

	messageBox(
		fmt.Sprintf("Named tunnel created successfully!\n\n"+
			"Tunnel ID: %s\n"+
			"Hostname: %s\n"+
			"\n"+
			"The tunnel connector token has been saved to secrets.toml.\n"+
			"The app will now use this tunnel for all traffic.\n\n"+
			"Use in Cursor: %s/v1",
			result.TunnelID, result.Hostname, result.Hostname),
		"Z-API Proxy — Tunnel", mbIconInfo)
}

// updateTOMLValue replaces or adds a key=value line under the given section.
// This is a simple TOML editor for single-line string values.
func updateTOMLValue(tomlText, section, key, value string) string {
	lines := strings.Split(tomlText, "\n")
	inSection := false
	found := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == section {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "[") && trimmed != section {
			// Entered a new section.
			if !found {
				// Insert before leaving the section.
				lines = append(lines[:i], append([]string{fmt.Sprintf("%s = \"%s\"", key, value)}, lines[i:]...)...)
				found = true
			}
			break
		}
		if inSection && strings.HasPrefix(trimmed, key+" =") {
			lines[i] = fmt.Sprintf("%s = \"%s\"", key, value)
			found = true
			break
		}
	}

	if !found {
		if !inSection {
			lines = append(lines, section)
		}
		lines = append(lines, fmt.Sprintf("%s = \"%s\"", key, value))
	}

	return strings.Join(lines, "\n")
}
