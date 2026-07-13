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

// progressWindow is the single standard mechanism, across the whole tray
// package, for showing a window with a status label and a marquee progress
// bar while a background operation runs, then morphing that SAME window in
// place into a result screen once the operation finishes. Exactly one
// native window is created per progressWindow: setStatus and finish only
// ever update its existing widgets — they never create another window.
//
// This exists because of a real, shipped bug: the previous implementation
// called declarative.MainWindow.Create() once (to grab widget handles
// before starting the background goroutine) and then called the
// declarative.MainWindow value's own Run() to pump messages. But
// declarative.MainWindow.Run() unconditionally calls Create() again
// internally (see lxn/walk/declarative/mainwindow.go) — building a
// completely separate second native window with its own copies of every
// widget, and reassigning AssignTo pointers to point at it. The first
// window stayed on screen, fully built and shown, but with no message loop
// ever pumping it: an orphaned "ghost" window next to the real, interactive
// one. That double window is what "Deploy Cloudflare Worker opens multiple
// progress windows" actually was — it reproduced on every single click, not
// just double-clicks, because it lived inside showProcessDialog itself, one
// layer below the (correctly-implemented) guarded()/mutex click-dedup.
//
// The fix: call the declarative MainWindow's Create() exactly once, then
// run the message loop via the ALREADY-CREATED native *walk.MainWindow's
// own Run() method (dlg.Run()) — never via the declarative struct again.
type progressWindow struct {
	dlg   *walk.MainWindow
	lbl   *walk.Label
	pb    *walk.ProgressBar
	okBtn *walk.PushButton
}

// newProgressWindow builds and shows a single native window with a status
// label and marquee progress bar. It does not pump the window's message
// loop — callers must follow up with (*progressWindow).run.
func newProgressWindow(title, message string) (*progressWindow, error) {
	pw := &progressWindow{}

	mw := MainWindow{
		AssignTo: &pw.dlg,
		Title:    title,
		MinSize:  Size{Width: 400, Height: 140},
		Size:     Size{Width: 450, Height: 160},
		Layout:   VBox{Margins: Margins{Left: 20, Top: 15, Right: 20, Bottom: 10}, Spacing: 8},
		Children: []Widget{
			Label{
				AssignTo: &pw.lbl,
				Text:     message + "...",
				MinSize:  Size{Height: 20},
			},
			ProgressBar{
				AssignTo:    &pw.pb,
				MinSize:     Size{Height: 18},
				MarqueeMode: true,
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{
						AssignTo:  &pw.okBtn,
						Text:      "OK",
						Enabled:   false,
						OnClicked: func() { pw.dlg.Close() },
					},
				},
			},
		},
	}

	// Exactly one Create() call for this window, ever. Do not follow this
	// with mw.Run() (see the progressWindow doc comment) — the message loop
	// is started by (*progressWindow).run, via pw.dlg.Run().
	if err := mw.Create(); err != nil {
		return nil, err
	}
	return pw, nil
}

// setStatus updates the status label of the already-open window on its UI
// thread. Safe to call any number of times during a single operation — it
// never creates a window, it only updates the existing one in place.
func (pw *progressWindow) setStatus(status string) {
	if pw == nil || pw.dlg == nil || pw.lbl == nil {
		return
	}
	pw.dlg.Synchronize(func() {
		pw.lbl.SetText(status)
	})
}

// finish morphs the window into its terminal state: progress bar full,
// title/label showing the result, and the OK button enabled so the user
// can dismiss it once they've read the summary. It updates the same window
// in place; it never creates a new one.
func (pw *progressWindow) finish(r ProcessResult) {
	if pw == nil || pw.dlg == nil {
		return
	}
	pw.dlg.Synchronize(func() {
		if pw.pb != nil {
			pw.pb.SetMarqueeMode(false)
			pw.pb.SetValue(100)
		}
		title := r.Title
		if !r.Success {
			title += " — Failed"
		}
		pw.dlg.SetTitle(title)
		if pw.lbl != nil {
			pw.lbl.SetText(r.Summary)
		}
		if pw.okBtn != nil {
			pw.okBtn.SetEnabled(true)
		}
	})
}

// run executes op in a background goroutine while pumping the window's
// message loop on the calling goroutine (walk requires the message loop to
// run on the thread that created the window). This is the only place that
// starts the loop, and it does so via pw.dlg.Run() — the native,
// already-created window's own Run method — never by re-invoking the
// declarative MainWindow's Run(), which would build a second window.
func (pw *progressWindow) run(op func(pw *progressWindow) ProcessResult) ProcessResult {
	resultCh := make(chan ProcessResult, 1)
	go func() {
		r := op(pw)
		pw.finish(r)
		resultCh <- r
	}()
	pw.dlg.Run()
	return <-resultCh
}

// showProcessDialog is the uniform, standard way for every tray action to
// show a progress window while a non-instant operation runs, and to push
// status updates to it. It shows a window with a marquee progress bar
// while op runs in a background goroutine; op reports progress through the
// supplied callback, which updates the SAME window's label in place (see
// progressWindow). On completion, the label updates to the result summary,
// the progress bar fills to 100%, and the OK button enables. Returns the
// ProcessResult so callers can branch on success/failure afterward (e.g.
// deciding whether to quit the app).
//
// Caller must guard against concurrent calls (e.g. TryLock at the call
// site, via t.guarded(...)) — this function itself only guarantees a single
// window per call, not dedup across overlapping calls.
func showProcessDialog(title, message string, op func(progress func(string)) ProcessResult) ProcessResult {
	pw, err := newProgressWindow(title, message)
	if err != nil {
		r := op(func(string) {})
		icon := walk.MsgBoxIconError
		if r.Success {
			icon = walk.MsgBoxIconInformation
		}
		walk.MsgBox(nil, r.Title, r.Summary, icon)
		return r
	}
	return pw.run(func(pw *progressWindow) ProcessResult {
		return op(pw.setStatus)
	})
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

// formatTunnelStartSummary creates a result summary for starting the quick
// (ephemeral-URL) public tunnel.
func formatTunnelStartSummary(url string) string {
	return fmt.Sprintf("Public tunnel is live!\n\n%s/v1\n\nUse this URL in Cursor:\nSettings → Models → OpenAI API Base URL", url)
}

// formatTunnelSummary creates a result summary for named tunnel creation.
func formatTunnelSummary(tunnelID, hostname string) string {
	return fmt.Sprintf("Named tunnel created successfully!\n\nTunnel ID: %s\nHostname: %s\n\n"+
		"The tunnel connector token has been saved to secrets.toml.\nThe app will now use this tunnel for all traffic.\n\n"+
		"Use in Cursor: %s/v1", tunnelID, hostname, hostname)
}
