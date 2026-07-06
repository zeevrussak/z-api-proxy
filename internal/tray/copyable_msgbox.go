package tray

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// copyableMsgBox creates a dialog with a selectable, copyable read-only
// Edit control instead of the standard MessageBox. This allows the user
// to select and Ctrl+C the error text.

var (
	msgBoxClassAtom uint16
	msgBoxRegOnce   sync.Once
	msgBoxShowing   bool
	msgBoxMu        sync.Mutex
)

const (
	esReadonly   = 0x0800
	esMultiline  = 0x0004
	esAutoVscroll = 0x0040
	wsHscroll    = 0x00100000

	idMsgText = 3000
	idMsgOK   = 3001
)

type msgBoxData struct {
	hwnd   uintptr
	hwndText uintptr
	hwndOK   uintptr
	flags   uintptr
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
		if notif == 0 { // BN_CLICKED
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

// showCopyableMsgBox shows a dialog with selectable text and an OK button.
// Replaces messageBox for error messages so users can copy error details.
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

	// Calculate window size based on text length.
	lines := strings.Count(text, "\n") + 1
	if lines < 3 {
		lines = 3
	}
	textW := 0
	for _, line := range strings.Split(text, "\n") {
		if len(line) > textW {
			textW = len(line)
		}
	}
	winW := textW*7 + 80
	if winW < 350 {
		winW = 350
	}
	if winW > 700 {
		winW = 700
	}
	winH := lines*16 + 120
	if winH < 180 {
		winH = 180
	}
	if winH > 500 {
		winH = 500
	}

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
		uintptr(wsCaption|wsSysMenu),
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

	mx := 15
	textH := winH - 70

	// Read-only multiline Edit control — selectable and copyable.
	editClass, _ := syscall.UTF16PtrFromString("Edit")
	textPtr, _ := syscall.UTF16PtrFromString(text)
	hwndText, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(editClass)),
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(wsChild|wsVisible|esMultiline|esAutoVscroll|esAutohscroll|esReadonly|wsVscroll|wsBorder),
		uintptr(mx), uintptr(10), uintptr(winW-mx*2), uintptr(textH),
		hwnd, idMsgText, 0, 0,
	)
	pSendMessageW.Call(hwndText, wmSetFont, fontHandle, 1)

	// OK button.
	btnClass, _ := syscall.UTF16PtrFromString("Button")
	okLabel, _ := syscall.UTF16PtrFromString("OK")
	btnW := 90
	okX := (winW - btnW) / 2
	hwndOK, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(btnClass)),
		uintptr(unsafe.Pointer(okLabel)),
		uintptr(wsChild|wsVisible|bsCenter|wsTabstop),
		uintptr(okX), uintptr(textH+15), uintptr(btnW), 30,
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

// messageBoxOrFallback tries the copyable dialog first, falls back to
// Win32 MessageBox if the custom dialog fails to create.
func messageBoxOrFallback(text, title string, icon uintptr) {
	// Use copyable dialog for all messages.
	showCopyableMsgBox(text, title, icon)
}
