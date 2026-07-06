// Package tray - walk-based settings dialog.
// Uses lxn/walk for native Windows widgets with automatic layout,
// DPI scaling, tab order, and resize handling.

package tray

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/pelletier/go-toml/v2"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/worker"
)

// showSettingsDialogWalk creates the settings dialog using lxn/walk.
func showSettingsDialogWalk(cfg *config.Config, configPath string) {
	// Walk requires a dedicated OS thread for its message loop.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var dlg *walk.MainWindow

	// Widget handles for reading values on save.
	var leListen, leBaseURL, leAPIKey, leGatewayKey *walk.LineEdit
	var leToken, leHostname, leAccountID, leAPIToken *walk.LineEdit
	var leWorkerName, leWorkerURL, leWorkerHost *walk.LineEdit
	var cbAPIStyle, cbTunnelMode *walk.ComboBox
	var chkLogging *walk.CheckBox
	var modelsLB *walk.ListBox

	models := cfg.Models
	apiStyles := []string{"OpenAI (chat/completions)", "Anthropic (messages)", "Both"}
	tunnelModes := []string{"Quick (ephemeral URL)", "Named (stable URL)"}

	curAPIStyle := 2 // "Both"
	curTunnelMode := 0
	if cfg.Tunnel.Mode == "named" {
		curTunnelMode = 1
	}

	_, err := MainWindow{
		AssignTo: &dlg,
		Title:    "Z-API Proxy — Settings",
		MinSize:  Size{Width: 500, Height: 600},
		Size:     Size{Width: 520, Height: 700},
		Layout:   VBox{Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 5}, Spacing: 6},
		Children: []Widget{
			ScrollView{
				Layout: VBox{Spacing: 6, MarginsZero: true},
				Children: []Widget{
					// ── Server ──
					GroupBox{
						Title:  "Server",
						Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							Label{Text: "Listen Address:"},
							LineEdit{AssignTo: &leListen, Text: cfg.Server.Listen},
						},
					},

					// ── Upstream ──
					GroupBox{
						Title:  "Upstream",
						Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							Label{Text: "Base URL:"},
							LineEdit{AssignTo: &leBaseURL, Text: cfg.Upstream.BaseURL},

							Label{Text: "API Key:"},
							LineEdit{AssignTo: &leAPIKey, Text: cfg.Upstream.APIKey, PasswordMode: true},

							Label{Text: "Gateway Key:"},
							LineEdit{AssignTo: &leGatewayKey, Text: cfg.Proxy.CursorKey, PasswordMode: true},

							Label{Text: "API Style:"},
							ComboBox{AssignTo: &cbAPIStyle, Model: apiStyles, CurrentIndex: curAPIStyle},

							Label{Text: ""},
							PushButton{
								Text: "Test Connection",
								OnClicked: func() {
									key := leAPIKey.Text()
									if key == "" {
										walk.MsgBox(dlg, "Z-API Proxy — Test", "No API key entered.", walk.MsgBoxIconWarning)
										return
									}
									wURL := strings.TrimSpace(leWorkerURL.Text())
									go testConnectionWalk(dlg, wURL, key, leBaseURL.Text())
								},
							},
						},
					},

					// ── Security ──
					GroupBox{
						Title:  "Security",
						Layout: VBox{Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							CheckBox{
								Text:    "Verify API Key — always enabled (required for security)",
								Checked: true,
								Enabled: false,
							},
						},
					},

					// ── Tunnel ──
					GroupBox{
						Title:  "Tunnel",
						Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							Label{Text: "Mode:"},
							ComboBox{AssignTo: &cbTunnelMode, Model: tunnelModes, CurrentIndex: curTunnelMode},

							Label{Text: "Token:"},
							LineEdit{AssignTo: &leToken, Text: cfg.Tunnel.Token, PasswordMode: true},

							Label{Text: "Hostname:"},
							LineEdit{AssignTo: &leHostname, Text: cfg.Tunnel.Hostname},
						},
					},

					// ── Cloudflare Worker ──
					GroupBox{
						Title:  "Cloudflare Worker",
						Layout: Grid{Columns: 2, Spacing: 6, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							Label{Text: "Account ID:"},
							LineEdit{AssignTo: &leAccountID, Text: cfg.Cloudflare.AccountID},

							Label{Text: "API Token:"},
							LineEdit{AssignTo: &leAPIToken, Text: cfg.Cloudflare.APIToken, PasswordMode: true},

							Label{Text: "Worker Name:"},
							LineEdit{AssignTo: &leWorkerName, Text: workerNameOrDefault(cfg)},

							Label{Text: "Worker URL:"},
							LineEdit{AssignTo: &leWorkerURL, Text: loadWorkerURL()},

							Label{Text: "Custom Domain:"},
							LineEdit{AssignTo: &leWorkerHost, Text: cfg.Cloudflare.WorkerHostname},

							CheckBox{
								AssignTo: &chkLogging,
								Text:    "Enable logging",
								Checked: cfg.Cloudflare.EnableLogging,
							},
						},
					},

					// ── Model Mappings ──
					GroupBox{
						Title:  "Model Mappings",
						Layout: VBox{Spacing: 4, Margins: Margins{Left: 8, Top: 4, Right: 8, Bottom: 4}},
						Children: []Widget{
							ListBox{
								AssignTo: &modelsLB,
								Model:    modelMappingModel{items: models},
								MinSize:  Size{Height: 120},
							},
							Composite{
								Layout: HBox{Spacing: 4, MarginsZero: true},
								Children: []Widget{
									PushButton{Text: "Add", OnClicked: func() {
										models = append(models, config.ModelMapping{
											From: "z.ai/new-model", To: "new-model",
										})
										modelsLB.SetModel(modelMappingModel{items: models})
									}},
									PushButton{Text: "Remove", OnClicked: func() {
										i := modelsLB.CurrentIndex()
										if i >= 0 && i < len(models) {
											models = append(models[:i], models[i+1:]...)
											modelsLB.SetModel(modelMappingModel{items: models})
										}
									}},
								},
							},
						},
					},
				},
			},

			// ── Bottom buttons ──
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "Save",
						OnClicked: func() {
							saveSettingsWalk(cfg, configPath,
								leListen.Text(), leBaseURL.Text(), leAPIKey.Text(),
								leGatewayKey.Text(), tunnelModeFromCombo(cbTunnelMode),
								leToken.Text(), leHostname.Text(),
								leAccountID.Text(), leAPIToken.Text(),
								leWorkerName.Text(), leWorkerURL.Text(),
								leWorkerHost.Text(),
								chkLogging.Checked(),
								models)
							apiStyle := apiStyleFromCombo(cbAPIStyle)
							saveApiModePref(apiStyle)
							dlg.Close()
						},
					},
					PushButton{
						Text: "Cancel",
						OnClicked: func() {
							dlg.Close()
						},
					},
				},
			},
		},
	}.Run()

	if err != nil {
		log.Printf("walk dialog error: %v — falling back to raw Win32", err)
		showSettingsDialog(cfg, configPath, nil)
	}
}

// workerNameOrDefault returns WorkerName or "z-api-proxy".
func workerNameOrDefault(cfg *config.Config) string {
	if cfg.Cloudflare.WorkerName != "" {
		return cfg.Cloudflare.WorkerName
	}
	return "z-api-proxy"
}

// tunnelModeFromCombo returns "quick" or "named".
func tunnelModeFromCombo(cb *walk.ComboBox) string {
	if cb == nil {
		return "quick"
	}
	i := cb.CurrentIndex()
	if i == 1 {
		return "named"
	}
	return "quick"
}

// apiStyleFromCombo returns "openai", "anthropic", or "both".
func apiStyleFromCombo(cb *walk.ComboBox) string {
	if cb == nil {
		return "both"
	}
	switch cb.CurrentIndex() {
	case 0:
		return "openai"
	case 1:
		return "anthropic"
	default:
		return "both"
	}
}

// saveApiModePref persists the API mode preference.
func saveApiModePref(mode string) {
	os.WriteFile(filepath.Join(config.AppConfigDir(), "api-mode.pref"), []byte(mode), 0600)
}

// loadApiModePref reads the saved API mode preference.
func loadApiModePref() string {
	data, err := os.ReadFile(filepath.Join(config.AppConfigDir(), "api-mode.pref"))
	if err != nil {
		return "both"
	}
	return strings.TrimSpace(string(data))
}

// modelMappingModel provides data for the ListBox.
type modelMappingModel struct {
	items []config.ModelMapping
}

func (m modelMappingModel) ItemCount() int { return len(m.items) }
func (m modelMappingModel) Value(i int) interface{} {
	if i < 0 || i >= len(m.items) {
		return ""
	}
	return fmt.Sprintf("%s → %s", m.items[i].From, m.items[i].To)
}

// saveSettingsWalk writes config + secrets from walk dialog values.
func saveSettingsWalk(cfg *config.Config, configPath string,
	listen, baseURL, apiKey, gatewayKey,
	tunnelMode, tunnelToken, tunnelHostname,
	cfAccountID, cfAPIToken, workerName, workerURL,
	workerHostname string,
	enableLogging bool,
	models []config.ModelMapping,
) {
	outCfg := &config.Config{
		Server:   config.ServerConfig{Listen: listen},
		Upstream: config.UpstreamConfig{BaseURL: baseURL},
		Tunnel: config.TunnelConfig{
			Mode:     tunnelMode,
			Hostname: tunnelHostname,
		},
		Security: config.SecurityConfig{VerifyKey: true},
		Cloudflare: config.CloudflareConfig{
			AccountID:      cfAccountID,
			WorkerName:     workerName,
			WorkerHostname: workerHostname,
			EnableLogging:  enableLogging,
		},
		Models: models,
	}

	if outCfg.Server.Listen == "" {
		outCfg.Server.Listen = "127.0.0.1:8787"
	}
	if outCfg.Upstream.BaseURL == "" {
		outCfg.Upstream.BaseURL = "https://api.z.ai/api/coding/paas/v4"
	}

	data, err := toml.Marshal(outCfg)
	if err != nil {
		return
	}
	os.WriteFile(configPath, data, 0600)

	// Write secrets.toml.
	secretsPath := config.DefaultSecretsPath()
	sec := struct {
		Upstream struct {
			APIKey string `toml:"api_key"`
		} `toml:"upstream"`
		Proxy struct {
			CursorKey string `toml:"cursor_key"`
		} `toml:"proxy"`
		Tunnel struct {
			Token string `toml:"token"`
		} `toml:"tunnel"`
		Cloudflare struct {
			APIToken string `toml:"api_token"`
		} `toml:"cloudflare"`
	}{}
	sec.Upstream.APIKey = apiKey
	sec.Proxy.CursorKey = gatewayKey
	sec.Tunnel.Token = tunnelToken
	sec.Cloudflare.APIToken = cfAPIToken

	secData, _ := toml.Marshal(sec)
	header := []byte("# Z-API Proxy Secrets — auto-generated by settings dialog.\n\n")
	os.WriteFile(secretsPath, append(header, secData...), 0600)

	// Worker URL preference.
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		saveWorkerURL(workerURL)
	} else {
		clearWorkerURL()
	}
}

// testConnectionWalk tests the connection from the walk dialog.
func testConnectionWalk(parent walk.Form, workerURL, apiKey, baseURL string) {
	if workerURL != "" {
		_, err := workerTestChat(workerURL, apiKey)
		if err != nil {
			walk.MsgBox(parent, "Z-API Proxy — Test", "Chat test failed:\n\n"+err.Error(), walk.MsgBoxIconError)
			return
		}
		walk.MsgBox(parent, "Z-API Proxy — Test", "Worker chat test successful!", walk.MsgBoxIconInformation)
		return
	}

	// Test upstream directly.
	testURL := strings.TrimSuffix(baseURL, "/") + "/models"
	_ = testURL
	// TODO: implement upstream test
	walk.MsgBox(parent, "Z-API Proxy — Test", "Direct upstream test not implemented yet.", walk.MsgBoxIconInformation)
}

// workerTestChat sends a test chat request through the Worker.
func workerTestChat(workerURL, apiKey string) (string, error) {
	return worker.TestChat(workerURL, apiKey)
}
