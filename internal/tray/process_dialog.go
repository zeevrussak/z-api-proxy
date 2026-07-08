package tray

import (
	"fmt"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// ProcessResult holds the outcome of a background operation.
type ProcessResult struct {
	Success bool
	Title   string
	Summary string
}

// showProcessDialog shows a window with a marquee progress bar while the
// operation runs in a background goroutine. On completion, the label updates
// to the summary, the progress bar fills to 100%, and the OK button enables.
//
// Caller must guard against concurrent calls (e.g. TryLock at the call site).
func showProcessDialog(title, message string, op func(progress func(string)) ProcessResult) {

	var dlg *walk.MainWindow
	var lbl *walk.Label
	var okBtn *walk.PushButton
	var pb *walk.ProgressBar

	mw := MainWindow{
		AssignTo: &dlg,
		Title:    title,
		MinSize:  Size{Width: 400, Height: 140},
		Size:     Size{Width: 450, Height: 160},
		Layout:   VBox{Margins: Margins{Left: 20, Top: 15, Right: 20, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Label{
				AssignTo: &lbl,
				Text:     message + "...",
				MinSize:  Size{Height: 20},
			},
			ProgressBar{
				AssignTo:    &pb,
				MinSize:     Size{Height: 18},
				MarqueeMode: true,
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

	if err := mw.Create(); err != nil {
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
				if pb != nil {
					pb.SetMarqueeMode(false)
					pb.SetValue(100)
				}
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
