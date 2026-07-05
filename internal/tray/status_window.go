package tray

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"z-api-proxy/internal/tunnel"
)

// Win32 procedures for the status window (reuses user32 from tray.go).
var (
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pEnableWindow     = user32.NewProc("EnableWindow")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pLoadCursorW      = user32.NewProc("LoadCursorW")
	pPostMessageW     = user32.NewProc("PostMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pSendMessageW     = user32.NewProc("SendMessageW")
	pSetWindowTextW   = user32.NewProc("SetWindowTextW")
	pShowWindow       = user32.NewProc("ShowWindow")

	gdi32            = syscall.NewLazyDLL("gdi32.dll")
	pGetStockObject  = gdi32.NewProc("GetStockObject")
)

// Window message and style constants.
const (
	wmCommand   = 0x0111
	wmDestroy   = 0x0002
	wmSetFont   = 0x0030
	wmApp       = 0x8000
	wmUrlReady  = wmApp + 0
	wmUrlFailed = wmApp + 1

	wsCaption  = 0x00C00000
	wsSysMenu  = 0x00080000
	wsChild    = 0x40000000
	wsVisible  = 0x10000000
	wsDisabled = 0x08000000

	ssCenter = 0x00000001
	bsCenter = 0x00000300

	swShow         = 5
	idcArrow       = 32512
	colorBtnFace   = 15
	defaultGuiFont = 17

	idStatic = 1000
	idCopy   = 1001
	idClose  = 1002
)

// winMsg matches the Windows MSG structure (48 bytes on x64).
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PtX     int32
	PtY     int32
}

// wndClassEx matches WNDCLASSEXW (80 bytes on x64).
type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

// tunnelWindowState holds the live state for the active tunnel status window.
type tunnelWindowState struct {
	hwnd      uintptr
	hwndText  uintptr
	hwndCopy  uintptr
	url       string
	errMsg    string
	clipboard string
	ready     bool
	tunnelMgr *tunnel.Manager
}

var (
	tunnelWinState *tunnelWindowState
	classAtom      uint16
	classRegOnce   sync.Once
	fontHandle     uintptr
	winGuardMu     sync.Mutex
	winShowing     bool
)

// ensureWindowClass registers the window class once.
func ensureWindowClass() {
	classRegOnce.Do(func() {
		fh, _, _ := pGetStockObject.Call(defaultGuiFont)
		fontHandle = fh

		wndProc := syscall.NewCallback(tunnelWindowProc)
		className, _ := syscall.UTF16PtrFromString("ZApiTunnelDlg")
		cursor, _, _ := pLoadCursorW.Call(0, idcArrow)

		wc := wndClassEx{
			Size:       uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:    wndProc,
			Background: colorBtnFace + 1,
			ClassName:  className,
			Cursor:     cursor,
		}
		atom, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		classAtom = uint16(atom)
	})
}

// tunnelWindowProc is the Win32 callback for the tunnel status window.
func tunnelWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		ctrlID := wParam & 0xFFFF
		notif := wParam >> 16
		if notif == 0 && tunnelWinState != nil { // BN_CLICKED
			switch ctrlID {
			case idCopy:
				if tunnelWinState.clipboard != "" {
					go copyToClip(tunnelWinState.clipboard)
				}
			case idClose:
				if !tunnelWinState.ready && tunnelWinState.tunnelMgr != nil {
					tunnelWinState.tunnelMgr.Stop()
				}
				pDestroyWindow.Call(hwnd)
			}
		}

	case wmUrlReady:
		if tunnelWinState != nil {
			tunnelWinState.ready = true
			text := fmt.Sprintf("Public tunnel is live!\r\n\r\n%s/v1\r\n\r\nPaste this in Cursor:\r\nSettings \u2192 Models \u2192 OpenAI API Base URL",
				tunnelWinState.url)
			setControlText(tunnelWinState.hwndText, text)
			tunnelWinState.clipboard = tunnelWinState.url + "/v1"
			pEnableWindow.Call(tunnelWinState.hwndCopy, 1)
		}

	case wmUrlFailed:
		if tunnelWinState != nil {
			setControlText(tunnelWinState.hwndText, "Failed to start tunnel:\r\n\r\n"+tunnelWinState.errMsg)
		}

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	ret, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

func setControlText(hwnd uintptr, text string) {
	ptr, _ := syscall.UTF16PtrFromString(text)
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(ptr)))
}

func copyToClip(text string) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		"$input | Set-Clipboard")
	cmd.Stdin = strings.NewReader(text)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Run()
}

// showTunnelWindow creates a status window, starts the tunnel, and runs
// a message loop until the user closes the window. Returns the URL and
// success flag.
func showTunnelWindow(tunnelMgr *tunnel.Manager) (string, bool) {
	winGuardMu.Lock()
	if winShowing {
		winGuardMu.Unlock()
		return "", false
	}
	winShowing = true
	winGuardMu.Unlock()
	defer func() {
		winGuardMu.Lock()
		winShowing = false
		winGuardMu.Unlock()
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ensureWindowClass()

	// Center on screen.
	sw, _, _ := pGetSystemMetrics.Call(0) // SM_CXSCREEN
	sh, _, _ := pGetSystemMetrics.Call(1) // SM_CYSCREEN
	winW, winH := 440, 180
	x := int32((int(sw) - winW) / 2)
	y := int32((int(sh) - winH) / 2)

	// Create main window.
	title, _ := syscall.UTF16PtrFromString("Z-API Proxy — Public Tunnel")
	hwnd, _, _ := pCreateWindowExW.Call(
		0, uintptr(classAtom), uintptr(unsafe.Pointer(title)),
		uintptr(wsCaption|wsSysMenu),
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return "", false
	}

	// Static text.
	staticClass, _ := syscall.UTF16PtrFromString("Static")
	initialText, _ := syscall.UTF16PtrFromString("Starting tunnel...\r\n\r\nPlease wait while the public tunnel is being created.")
	hwndText, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(initialText)),
		uintptr(wsChild|wsVisible|ssCenter),
		20, 15, 400, 80,
		hwnd, idStatic, 0, 0,
	)

	// Copy button (initially disabled).
	btnClass, _ := syscall.UTF16PtrFromString("Button")
	copyLabel, _ := syscall.UTF16PtrFromString("Copy Base URL")
	hwndCopy, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(copyLabel)),
		uintptr(wsChild|wsVisible|bsCenter|wsDisabled),
		150, 110, 140, 32,
		hwnd, idCopy, 0, 0,
	)

	// Close button.
	closeLabel, _ := syscall.UTF16PtrFromString("Close")
	_, _, _ = pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(closeLabel)),
		uintptr(wsChild|wsVisible|bsCenter),
		310, 110, 80, 32,
		hwnd, idClose, 0, 0,
	)

	// Set modern font.
	pSendMessageW.Call(hwndText, wmSetFont, fontHandle, 1)
	pSendMessageW.Call(hwndCopy, wmSetFont, fontHandle, 1)

	// Store state for the window proc.
	tunnelWinState = &tunnelWindowState{
		hwnd:      hwnd,
		hwndText:  hwndText,
		hwndCopy:  hwndCopy,
		tunnelMgr: tunnelMgr,
	}

	pShowWindow.Call(hwnd, swShow)

	// Start tunnel in a goroutine; post results to the window.
	go func() {
		url, err := tunnelMgr.Start()
		if err != nil {
			if tunnelWinState != nil {
				tunnelWinState.errMsg = err.Error()
			}
			pPostMessageW.Call(hwnd, wmUrlFailed, 0, 0)
			return
		}
		if tunnelWinState != nil {
			tunnelWinState.url = url
		}
		pPostMessageW.Call(hwnd, wmUrlReady, 0, 0)
	}()

	// Message loop (blocks until window is closed).
	var m winMsg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break
		}
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	if tunnelWinState != nil && tunnelWinState.ready {
		return tunnelWinState.url, true
	}
	return "", false
}
