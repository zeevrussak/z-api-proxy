package tray

import (
	"fmt"
	"runtime"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// ProcessResult holds the outcome of a background operation.
type ProcessResult struct {
	Success  bool
	Title    string
	Summary  string
}

// showProcessDialog shows a dialog that displays "Working..." while the
// operation runs in a goroutine, then switches to a summary with an OK button.
// The operation function receives a progress callback for status updates.
func showProcessDialog(title, message string, op func(progress func(string)) ProcessResult) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var dlg *walk.MainWindow
	var lbl *walk.Label
	var okBtn *walk.PushButton

	result := make(chan ProcessResult, 1)

	_, err := MainWindow{
		AssignTo: &dlg,
		Title:    title,
		MinSize:  Size{Width: 400, Height: 130},
		Size:     Size{Width: 450, Height: 150},
		Layout:   VBox{Margins: Margins{Left: 20, Top: 15, Right: 20, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Label{
				AssignTo: &lbl,
				Text:     message + "...",
				MinSize:  Size{Height: 30},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo: &okBtn,
						Text:     "OK",
						Enabled:  false,
						OnClicked: func() {
							dlg.Close()
						},
					},
				},
			},
		},
	}.Run()

	if err != nil {
		// Fallback: run synchronously and show result via MsgBox.
		r := op(func(s string) {})
		var icon walk.MsgBoxStyle
		if r.Success {
			icon = walk.MsgBoxIconInformation
		} else {
			icon = walk.MsgBoxIconError
		}
		walk.MsgBox(nil, r.Title, r.Summary, icon)
		return
	}

	// Start the operation in a goroutine.
	go func() {
		r := op(func(status string) {
			if dlg != nil && lbl != nil {
				dlg.Synchronize(func() {
					lbl.SetText(status)
				})
			}
		})
		result <- r
	}()

	// Poll for completion using a timer.
	go func() {
		select {
		case r := <-result:
			if dlg == nil {
				return
			}
			dlg.Synchronize(func() {
				if r.Success {
					dlg.SetTitle(r.Title)
					lbl.SetText(r.Summary)
				} else {
					dlg.SetTitle(r.Title + " — Failed")
					lbl.SetText(r.Summary)
				}
				if okBtn != nil {
					okBtn.SetEnabled(true)
				}
			})
		case <-time.After(120 * time.Second):
			if dlg != nil {
				dlg.Synchronize(func() {
					lbl.SetText("Operation timed out.")
					if okBtn != nil {
						okBtn.SetEnabled(true)
					}
				})
			}
		}
	}()
}

// formatDeploySummary creates a result summary for Worker deployment.
func formatDeploySummary(url string, customDomain string) string {
	msg := fmt.Sprintf("Cloudflare Worker deployed!\n\nURL: %s/v1", url)
	if customDomain != "" {
		msg += "\nCustom domain: " + customDomain
	}
	msg += "\n\nSecrets set: API_KEY, CURSOR_KEY, TEST_KEY\n"
	msg += "Variables set: UPSTREAM, MODEL_MAPPINGS, MODEL_REVERSE"
	return msg
}

// formatRegisterSummary creates a result summary for Cursor registration.
func formatRegisterSummary(path string, count int) string {
	return fmt.Sprintf("Models registered in Cursor!\n\nSettings: %s\nModels: %d z.ai GLM models\n\nNext: restart Cursor and select a model.", path, count)
}

// formatTunnelSummary creates a result summary for tunnel creation.
func formatTunnelSummary(hostname string) string {
	return fmt.Sprintf("Named tunnel created!\n\nHostname: %s\nToken saved to secrets.toml", hostname)
}
