package tray

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// copyableMsgBox creates a dialog with a selectable, copyable read-only
// Edit control instead of the standard MessageBox.

var (
	msgBoxClassAtom uint16
	msgBoxRegOnce   sync.Once
	msgBoxShowing   bool
	msgBoxMu        sync.Mutex

	pAdjustWindowRectEx = user32.NewProc("AdjustWindowRectEx")
	pGetDpiForWindow    = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem    = user32.NewProc("GetDpiForSystem")
)

const (
	esReadonly    = 0x0800
	esMultiline   = 0x0004
	esAutoVscroll = 0x0040

	idMsgText = 3000
	idMsgOK   = 3001
)

type msgBoxData struct {
	hwnd     uintptr
	hwndText uintptr
	hwndOK   uintptr
	flags    uintptr
}

var currentMsgBox *msgBoxData

func ensureMsgBoxClass() {
	msgBoxRegOnce.Do(func() {
		wndProc := syscall.NewCallback(msgBoxWndProc)
		className, _ := syscall.UTF16PtrFromString("ZApiMsgBox")
		cursor, _, _ := pLoadCursorW.Call(0, idcArrow)

		wc := wndClassEx{
			Size:       uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:    wndProc,
			Background: colorBtnFace + 1,
			ClassName:  className,
			Cursor:     cursor,
		}
		atom, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		msgBoxClassAtom = uint16(atom)
	})
}

func msgBoxWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		ctrlID := wParam & 0xFFFF
		notif := wParam >> 16
		if notif == 0 {
			if ctrlID == idMsgOK {
				pDestroyWindow.Call(hwnd)
			}
		}

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	ret, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

// dpiScale returns the DPI scale factor (1.0 = 96 DPI, 1.5 = 144 DPI, etc.)
func dpiScale() float64 {
	dpi, _, _ := pGetDpiForSystem.Call()
	if dpi == 0 {
		return 1.0
	}
	return float64(dpi) / 96.0
}

// showCopyableMsgBox shows a dialog with selectable text and an OK button.
func showCopyableMsgBox(text, title string, icon uintptr) {
	msgBoxMu.Lock()
	if msgBoxShowing {
		msgBoxMu.Unlock()
		return
	}
	msgBoxShowing = true
	msgBoxMu.Unlock()
	defer func() {
		msgBoxMu.Lock()
		msgBoxShowing = false
		msgBoxMu.Unlock()
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ensureMsgBoxClass()

	scale := dpiScale()

	// DPI-aware sizing constants.
	margin := int(12 * scale)
	spacing := int(8 * scale)
	btnH := int(28 * scale)
	btnW := int(90 * scale)
	editTop := int(10 * scale)

	// Calculate text area dimensions from content.
	lines := strings.Count(text, "\n") + 1
	if lines < 3 {
		lines = 3
	}
	lineH := int(16 * scale)
	maxLineWidth := 0
	for _, line := range strings.Split(text, "\n") {
		w := len(line) * int(7*scale)
		if w > maxLineWidth {
			maxLineWidth = w
		}
	}

	// Client area dimensions (the area inside the window, excluding title bar/borders).
	clientW := maxLineWidth + margin*2
	if clientW < int(320*scale) {
		clientW = int(320 * scale)
	}
	if clientW > int(650*scale) {
		clientW = int(650 * scale)
	}

	textH := lines * lineH
	if textH > int(300*scale) {
		textH = int(300 * scale)
	}

	bottomArea := btnH + spacing*2 // button row + padding above and below
	clientH := editTop + textH + bottomArea
	if clientH < int(150*scale) {
		clientH = int(150 * scale)
	}

	// Calculate window rect from client rect (adds title bar + borders).
	rect := struct{ Left, Top, Right, Bottom int32 }{
		0, 0, int32(clientW), int32(clientH),
	}
	style := wsCaption | wsSysMenu
	pAdjustWindowRectEx.Call(
		uintptr(unsafe.Pointer(&rect)),
		uintptr(style), 0, 0,
	)
	winW := int(rect.Right - rect.Left)
	winH := int(rect.Bottom - rect.Top)

	// Center on screen.
	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	x := int32((int(sw) - winW) / 2)
	y := int32((int(sh) - winH) / 2)

	titlePtr, _ := syscall.UTF16PtrFromString(title)
	const wsExControlParent = 0x00010000
	hwnd, _, _ := pCreateWindowExW.Call(
		wsExControlParent,
		uintptr(msgBoxClassAtom),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return
	}

	// Set icon.
	if hBigIcon != 0 {
		pSendMessageW.Call(hwnd, wmSetIcon, 1, hBigIcon)
	}
	if hSmallIcon != 0 {
		pSendMessageW.Call(hwnd, wmSetIcon, 0, hSmallIcon)
	}

	// Edit control — read-only multiline, selectable.
	editClass, _ := syscall.UTF16PtrFromString("Edit")
	textPtr, _ := syscall.UTF16PtrFromString(text)
	hwndText, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(editClass)),
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(wsChild|wsVisible|esMultiline|esAutoVscroll|esAutohscroll|esReadonly|wsVscroll|wsBorder),
		uintptr(margin), uintptr(editTop), uintptr(clientW-margin*2), uintptr(textH),
		hwnd, idMsgText, 0, 0,
	)
	pSendMessageW.Call(hwndText, wmSetFont, fontHandle, 1)

	// OK button — centered horizontally, below the edit with padding.
	btnClass, _ := syscall.UTF16PtrFromString("Button")
	okLabel, _ := syscall.UTF16PtrFromString("OK")
	btnX := (clientW - btnW) / 2
	btnY := editTop + textH + spacing
	hwndOK, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnClass)),
		uintptr(unsafe.Pointer(okLabel)),
		uintptr(wsChild|wsVisible|bsCenter|wsTabstop),
		uintptr(btnX), uintptr(btnY), uintptr(btnW), uintptr(btnH),
		hwnd, idMsgOK, 0, 0,
	)
	pSendMessageW.Call(hwndOK, wmSetFont, fontHandle, 1)

	currentMsgBox = &msgBoxData{
		hwnd:     hwnd,
		hwndText: hwndText,
		hwndOK:   hwndOK,
		flags:    icon,
	}

	pShowWindow.Call(hwnd, swShow)

	var m winMsg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break
		}
		processed, _, _ := pIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m)))
		if processed != 0 {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func messageBoxOrFallback(text, title string, icon uintptr) {
	showCopyableMsgBox(text, title, icon)
}
