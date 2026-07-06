// Package tray - walk-based settings dialog.
// Replaces raw Win32 control positioning with walk's layout engine
// for automatic DPI scaling, tab order, and resize handling.

package tray

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/pelletier/go-toml/v2"

	"z-api-proxy/internal/config"
)

// showSettingsDialogWalk creates the settings dialog using lxn/walk.
// This provides native Windows widgets with automatic layout, DPI
// scaling, tab order, and scroll — replacing the fragile raw Win32
// control positioning.
func showSettingsDialogWalk(cfg *config.Config, configPath string) {
	var dlg *walk.Dialog
	var db *walk.DataBinder

	// Mutable copies for the form.
	listen := cfg.Server.Listen
	baseURL := cfg.Upstream.BaseURL
	apiKey := cfg.Upstream.APIKey
	gatewayKey := cfg.Proxy.CursorKey
	verifyKey := true // always on
	tunnelMode := cfg.Tunnel.Mode
	if tunnelMode == "" {
		tunnelMode = "quick"
	}
	tunnelToken := cfg.Tunnel.Token
	tunnelHostname := cfg.Tunnel.Hostname
	cfAccountID := cfg.Cloudflare.AccountID
	cfAPIToken := cfg.Cloudflare.APIToken
	workerName := cfg.Cloudflare.WorkerName
	if workerName == "" {
		workerName = "z-api-proxy"
	}
	workerURL := loadWorkerURL()
	workerHostname := cfg.Cloudflare.WorkerHostname
	enableLogging := cfg.Cloudflare.EnableLogging

	// Model mappings for the list box.
	var modelsLB *walk.ListBox
	models := cfg.Models

	apiStyles := []string{"OpenAI (chat/completions)", "Anthropic (messages)", "Both"}
	tunnelModes := []string{"Quick (ephemeral URL)", "Named (stable URL)"}

	_, err := Dialog{
		AssignTo: &dlg,
		Title:    "Z-API Proxy — Settings",
		MinSize:  Size{Width: 520, Height: 650},
		Layout: VBox{
			MarginsZero: false,
			Spacing:     8,
		},
		Children: []Widget{
			// Scroll container for all settings.
			ScrollView{
				Layout: VBox{Spacing: 6},
				Children: []Widget{
					// Server section.
					GroupBox{
						Title:  "Server",
						Layout: Grid{Columns: 2, Spacing: 8},
						Children: []Widget{
							Label{Text: "Listen Address:"},
							LineEdit{Text: Bind("Listen")},
						},
					},

					// Upstream section.
					GroupBox{
						Title:  "Upstream",
						Layout: Grid{Columns: 2, Spacing: 8},
						Children: []Widget{
							Label{Text: "Base URL:"},
							LineEdit{Text: Bind("BaseURL"), AssignTo: nil},

							Label{Text: "API Key:"},
							Composite{
								Layout: HBox{Spacing: 4, MarginsZero: true},
								Children: []Widget{
									LineEdit{Text: Bind("APIKey"), PasswordMode: true},
									PushButton{Text: "Test Connection", OnClicked: func() {
										// TODO: wire to testConnectionFromSettings
									}},
								},
							},

							Label{Text: "Gateway Key:"},
							LineEdit{Text: Bind("GatewayKey"), PasswordMode: true},

							Label{Text: "API Style:"},
							ComboBox{
								Model:    apiStyles,
								CurrentIndex: 2, // "Both" by default
							},
						},
					},

					// Security section.
					GroupBox{
						Title:  "Security",
						Layout: VBox{},
						Children: []Widget{
							CheckBox{
								Text:     "Verify API Key — always enabled (required for security)",
								Checked:  Bind("VerifyKey"),
								Enabled:  false, // always on, shown but disabled
							},
						},
					},

					// Tunnel section.
					GroupBox{
						Title:  "Tunnel",
						Layout: Grid{Columns: 2, Spacing: 8},
						Children: []Widget{
							Label{Text: "Mode:"},
							ComboBox{
								Model:    tunnelModes,
								BindingMember: "mode",
							},

							Label{Text: "Token:"},
							LineEdit{Text: Bind("TunnelToken"), PasswordMode: true},

							Label{Text: "Hostname:"},
							LineEdit{Text: Bind("TunnelHostname")},
						},
					},

					// Cloudflare Worker section.
					GroupBox{
						Title:  "Cloudflare Worker",
						Layout: Grid{Columns: 2, Spacing: 8},
						Children: []Widget{
							Label{Text: "Account ID:"},
							LineEdit{Text: Bind("AccountID")},

							Label{Text: "API Token:"},
							LineEdit{Text: Bind("APIToken"), PasswordMode: true},

							Label{Text: "Worker Name:"},
							LineEdit{Text: Bind("WorkerName")},

							Label{Text: "Worker URL:"},
							LineEdit{Text: Bind("WorkerURL")},

							Label{Text: "Custom Domain:"},
							LineEdit{Text: Bind("WorkerHostname")},

							CheckBox{
								Text:    "Enable logging",
								Checked: Bind("EnableLogging"),
							},
						},
					},

					// Model Mappings section.
					GroupBox{
						Title:  "Model Mappings",
						Layout: VBox{Spacing: 4},
						Children: []Widget{
							ListBox{
								AssignTo: &modelsLB,
								Model:    modelMappingModel{items: models},
								OnItemActivated: func() {
									// TODO: edit model
								},
							},
							Composite{
								Layout: HBox{Spacing: 4},
								Children: []Widget{
									PushButton{Text: "Add", OnClicked: func() {
										models = append(models, config.ModelMapping{
											From: "z.ai/new-model",
											To:   "new-model",
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

			// Bottom buttons — always visible, outside scroll.
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text:    "Save",
						OnClicked: func() {
							if db != nil {
								db.Submit()
							}
							saveSettingsWalk(cfg, configPath, listen, baseURL, apiKey,
								gatewayKey, tunnelMode, tunnelToken, tunnelHostname,
								cfAccountID, cfAPIToken, workerName, workerURL,
								workerHostname, enableLogging, models)
							dlg.Accept()
						},
					},
					PushButton{
						Text: "Cancel",
						OnClicked: func() {
							dlg.Cancel()
						},
					},
				},
			},
		},
	}.Run(nil)

	if err != nil {
		// Fallback to raw Win32 dialog if walk fails.
		showSettingsDialog(cfg, configPath, nil)
	}
	_ = db
	_ = verifyKey
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
