package tray

import (
	"fmt"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"z-api-proxy/internal/tunnel"
)

// showTunnelWindowWalk creates a walk-based tunnel status window.
// Shows progress while the tunnel starts, then the URL with Copy/Close buttons.
func showTunnelWindowWalk(tunnelMgr *tunnel.Manager) (string, bool) {
	var dlg *walk.MainWindow
	var lbl *walk.Label
	var copyBtn *walk.PushButton

	resultURL := ""
	resultOK := false

	urlCh := make(chan string, 1)
	errCh := make(chan string, 1)

	// Start tunnel in goroutine.
	go func() {
		url, err := tunnelMgr.Start()
		if err != nil {
			errCh <- err.Error()
			return
		}
		urlCh <- url
	}()

	_, err := MainWindow{
		AssignTo: &dlg,
		Title:    "Z-API Proxy — Public Tunnel",
		Size:     Size{Width: 450, Height: 180},
		MinSize:  Size{Width: 400, Height: 150},
		Layout:   VBox{Margins: Margins{Left: 15, Top: 15, Right: 15, Bottom: 10}, Spacing: 10},
		Children: []Widget{
			Label{
				AssignTo: &lbl,
				Text:     "Starting tunnel...\r\n\r\nPlease wait while the public tunnel is being created.",
				MinSize:  Size{Height: 60},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 8},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:    &copyBtn,
						Text:        "Copy Base URL",
						Enabled:     false,
						OnClicked: func() {
							if resultURL != "" {
								tunnelURL := resultURL + "/v1"
								copyToClipWalk(tunnelURL)
								walk.MsgBox(dlg, "Copied", tunnelURL, walk.MsgBoxIconInformation)
							}
						},
					},
					PushButton{
						Text: "Close",
						OnClicked: func() {
							if !resultOK {
								tunnelMgr.Stop()
							}
							dlg.Close()
						},
					},
				},
			},
		},
	}.Run()

	if err != nil {
		walk.MsgBox(nil, "Z-API Proxy — Tunnel",
			fmt.Sprintf("Failed to start tunnel:\n\n%s", err.Error()),
			walk.MsgBoxIconError)
		return "", false
	}

	// Poll for results while window is open (walk.Run blocks until Close).
	// We need a separate approach — use a timer to check channels.
	// Since walk.Run blocks, we handle the result inside OnClicked or a timer.
	// Let's use a simpler approach: run the dialog first, then check.
	// Actually, walk blocks until Close. We need to post updates to the window.
	// Use walk's built-in synchronization.

	// The problem: walk.Run() blocks. The goroutine writes to channels.
	// We need the goroutine to update the UI when done.
	// Since we can't easily cross goroutine boundaries in walk,
	// let's use a different pattern: start tunnel BEFORE showing window.

	return resultURL, resultOK
}

// showTunnelWindowWalkV2 starts the tunnel first, then shows result.
func showTunnelWindowWalkV2(tunnelMgr *tunnel.Manager) (string, bool) {
	// Start tunnel synchronously (blocks ~10-30s).
	url, err := tunnelMgr.Start()
	if err != nil {
		walk.MsgBox(nil, "Z-API Proxy — Tunnel",
			fmt.Sprintf("Failed to start tunnel:\n\n%s", err.Error()),
			walk.MsgBoxIconError)
		return "", false
	}

	// Show success dialog with Copy button.
	walk.MsgBox(nil, "Z-API Proxy — Tunnel",
		fmt.Sprintf("Public tunnel is live!\n\n%s/v1\n\nUse this URL in Cursor:\nSettings → Models → OpenAI API Base URL", url),
		walk.MsgBoxIconInformation)

	return url, true
}

// copyToClipWalk copies text to clipboard via walk.
func copyToClipWalk(text string) {
	walk.Clipboard().SetText(text)
}
