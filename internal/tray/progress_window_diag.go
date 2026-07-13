package tray

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	pEnumWindows     = user32.NewProc("EnumWindows")
	pGetWindowTextW  = user32.NewProc("GetWindowTextW")
	pIsWindowVisible = user32.NewProc("IsWindowVisible")
	pPostMessage     = user32.NewProc("PostMessageW")
)

const wmClose = 0x0010

// countWindowsWithTitle returns how many currently visible top-level
// windows have exactly the given title. It exists solely to make the
// duplicate-progress-window bug mechanically verifiable end-to-end (see
// ShowProcessDialogForTest): rather than eyeballing screenshots, a test can
// assert this never returns more than 1 while an operation is running.
func countWindowsWithTitle(title string) int {
	count := 0
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !isVisibleWindowWithTitle(hwnd, title) {
			return 1 // keep enumerating
		}
		count++
		return 1 // keep enumerating
	})
	pEnumWindows.Call(cb, 0)
	return count
}

// closeWindowByTitle finds the first visible top-level window with exactly
// the given title and posts WM_CLOSE to it — the same message Windows
// sends when a user clicks a window's own close button. Returns false if
// no matching window currently exists.
func closeWindowByTitle(title string) bool {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if !isVisibleWindowWithTitle(hwnd, title) {
			return 1 // keep enumerating
		}
		found = hwnd
		return 0 // stop enumerating, we found it
	})
	pEnumWindows.Call(cb, 0)
	if found == 0 {
		return false
	}
	pPostMessage.Call(found, wmClose, 0, 0)
	return true
}

func isVisibleWindowWithTitle(hwnd uintptr, title string) bool {
	visible, _, _ := pIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return false
	}
	buf := make([]uint16, len(title)+16)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return n > 0 && syscall.UTF16ToString(buf[:n]) == title
}

// ShowProcessDialogForTest drives a real showProcessDialog operation — the
// exact same production function deployWorker, registerModels,
// testConnection, toggleTunnel, createNamedTunnel, and installUpdate all
// call — through several progress updates, counting the number of live
// top-level windows carrying the dialog's own title via
// countWindowsWithTitle on each update. It fails if that count is ever
// more than one at a time, or if the window was never observed at all
// (which would mean the check itself is broken, e.g. no desktop session
// available).
//
// This exercises the exact defect that shipped: declarative.MainWindow.Run()
// was called on a MainWindow value that had already been Create()'d, which
// unconditionally re-invokes Create() internally and opens a second native
// window — on every single invocation, not just concurrent ones. There is
// no user present to click OK/close the dialog in this automated run, so a
// second goroutine posts WM_CLOSE to it (the same message Windows sends on
// a user's own close click) once the operation's progress updates are done.
//
// Used by the --test-progress CLI flag and by the automated UI test in
// main_test.go (both require a real Windows desktop session, mirroring
// ShowSettingsForTest/TestUISettingsWindow).
func ShowProcessDialogForTest() bool {
	const title = "Z-API Proxy — Progress Window Count Test"

	var mu sync.Mutex
	observed := false
	maxSeen := 0
	updatesDone := make(chan struct{})

	go func() {
		<-updatesDone
		for i := 0; i < 100; i++ {
			if closeWindowByTitle(title) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	showProcessDialog(title, "Running", func(progress func(string)) ProcessResult {
		for i := 0; i < 6; i++ {
			progress(fmt.Sprintf("Step %d of 6...", i+1))

			n := countWindowsWithTitle(title)
			mu.Lock()
			if n > 0 {
				observed = true
			}
			if n > maxSeen {
				maxSeen = n
			}
			mu.Unlock()

			time.Sleep(80 * time.Millisecond)
		}
		close(updatesDone)
		return ProcessResult{Success: true, Title: title, Summary: "done"}
	})

	mu.Lock()
	defer mu.Unlock()
	return observed && maxSeen == 1
}
