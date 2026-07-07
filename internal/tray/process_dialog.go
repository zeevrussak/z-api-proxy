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
	Success bool
	Title   string
	Summary string
}

// showProcessDialog shows a window with a status label while the operation
// runs in a background goroutine. On completion, the label updates to the
// summary and the OK button enables. The operation can call the progress
// callback to update the status text live.
func showProcessDialog(title, message string, op func(progress func(string)) ProcessResult) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var dlg *walk.MainWindow
	var lbl *walk.Label
	var okBtn *walk.PushButton

	// Build and create the window (does not block — Run blocks below).
	mw := MainWindow{
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
						AssignTo:  &okBtn,
						Text:      "OK",
						Enabled:   false,
						OnClicked: func() { dlg.Close() },
					},
				},
			},
		},
	}

	// Create the window first (without running the message loop).
	if err := mw.Create(); err != nil {
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

	// Now the window exists (dlg is populated). Start the operation in
	// a goroutine — it can update the label via Synchronize.
	go func() {
		r := op(func(status string) {
			if dlg != nil && lbl != nil {
				dlg.Synchronize(func() {
					lbl.SetText(status)
				})
			}
		})

		// Update UI with result.
		if dlg != nil {
			dlg.Synchronize(func() {
				if r.Success {
					dlg.SetTitle(r.Title)
				} else {
					dlg.SetTitle(r.Title + " — Failed")
				}
				if lbl != nil {
					lbl.SetText(r.Summary)
				}
				if okBtn != nil {
					okBtn.SetEnabled(true)
				}
			})
		}
	}()

	// Run the message loop — blocks until OK clicked → dlg.Close().
	mw.Run()
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

// StartProcessDialog creates the process dialog and returns immediately.
// Used by test code to verify the dialog lifecycle.
// Returns the window handle and a completion channel.
func StartProcessDialog(title, message string, op func(progress func(string)) ProcessResult) (*walk.MainWindow, chan ProcessResult) {
	var dlg *walk.MainWindow
	var lbl *walk.Label
	var okBtn *walk.PushButton
	done := make(chan ProcessResult, 1)

	mw := MainWindow{
		AssignTo: &dlg,
		Title:    title,
		MinSize:  Size{Width: 400, Height: 130},
		Size:     Size{Width: 450, Height: 150},
		Layout:   VBox{Margins: Margins{Left: 20, Top: 15, Right: 20, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Label{AssignTo: &lbl, Text: message + "...", MinSize: Size{Height: 30}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:  &okBtn,
						Text:      "OK",
						Enabled:   false,
						OnClicked: func() { dlg.Close() },
					},
				},
			},
		},
	}

	if err := mw.Create(); err != nil {
		go func() {
			r := op(func(s string) {})
			done <- r
		}()
		return nil, done
	}

	go func() {
		r := op(func(status string) {
			if dlg != nil && lbl != nil {
				dlg.Synchronize(func() {
					lbl.SetText(status)
				})
			}
		})

		if dlg != nil {
			dlg.Synchronize(func() {
				if r.Success {
					dlg.SetTitle(r.Title)
				} else {
					dlg.SetTitle(r.Title + " — Failed")
				}
				if lbl != nil {
					lbl.SetText(r.Summary)
				}
				if okBtn != nil {
					okBtn.SetEnabled(true)
				}
			})
		}
		done <- r
	}()

	// Auto-close after 5 seconds for testing.
	go func() {
		time.Sleep(5 * time.Second)
		if dlg != nil {
			dlg.Synchronize(func() {
				dlg.Close()
			})
		}
	}()

	return dlg, done
}
