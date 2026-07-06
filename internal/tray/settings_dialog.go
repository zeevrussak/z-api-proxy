package tray

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/pelletier/go-toml/v2"

	"z-api-proxy/internal/config"
)

// Additional Win32 procedures for the settings dialog.
var (
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pGetWindowLong        = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLong        = user32.NewProc("SetWindowLongPtrW")
	pLoadIconW            = user32.NewProc("LoadIconW")
	pLoadImageW           = user32.NewProc("LoadImageW")
	pMoveWindow           = user32.NewProc("MoveWindow")
	pInvalidateRect       = user32.NewProc("InvalidateRect")
	pTranslateMessage     = user32.NewProc("TranslateMessage")
	pIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
)

// Additional style and message constants.
const (
	esAutohscroll    = 0x0080
	esPassword       = 0x0020
	lbsNotify        = 0x0001
	lbAddString      = 0x0180
	lbDeleteString   = 0x0182
	lbGetCount       = 0x018B
	lbGetCurSel      = 0x0188
	bmGetCheck       = 0x00F0
	bmSetCheck       = 0x00F1
	bstChecked       = 1
	wsVscroll        = 0x00200000
	wsBorder         = 0x00800000
	wsThickframe     = 0x00040000
	wsMinimizeBox    = 0x00020000
	wsMaximizeBox    = 0x00010000
	wsTabstop        = 0x00010000

	gwlStyle          = ^uintptr(15) // -16 as uintptr (GWL_STYLE)
	bsAutocheckbox    = 0x00000003
	bsAutoradioButton = 0x00000009

	wmSize      = 0x0005
	wmSetIcon   = 0x0080
	wmMouseWheel = 0x020A
	wsGroup       = 0x00020000
	wmVscroll    = 0x0115
	sizeRestored = 0

	sbLineUp   = 0
	sbLineDown = 1
	sbVert     = 1
	wheelDelta = 120

	// LoadImage constants
	lrDefaultSize = 0x0040
	imageIcon     = 1

	// Control IDs
	idListenEd     = 2000
	idBaseURLEd    = 2001
	idAPIKeyEd     = 2002
	idVerifyChk    = 2003
	idTunnelQuick  = 2004
	idTunnelNamed  = 2005
	idTokenEd      = 2006
	idHostnameEd   = 2007
	idAcctIDEd     = 2008
	idAPITokenEd   = 2009
	idWorkerNameEd = 2010
	idModelsList   = 2011
	idModelAdd     = 2012
	idModelRemove  = 2013
	idShowKey      = 2014
	idSave         = 2015
	idCancel       = 2016
)

// layoutControl describes a control that needs to be repositioned on resize.
type layoutControl struct {
	hwnd       uintptr
	flags      layoutFlags
	fixedH     uintptr // fixed height (0 = stretch)
}

type layoutFlags int

const (
	lfNone      layoutFlags = 0
	lfStretch   layoutFlags = 1 << iota // stretch width to fill
	lfAnchorBottom                       // anchor to bottom of window
	lfFullWidth                          // full width minus margins
)

// settingsDialogState holds control handles and config for the dialog.
type settingsDialogState struct {
	hwnd           uintptr
	configPath     string
	cfg            *config.Config
	hwndListen     uintptr
	hwndBaseURL    uintptr
	hwndAPIKey     uintptr
	hwndVerify     uintptr
	hwndQuick      uintptr
	hwndNamed      uintptr
	hwndToken      uintptr
	hwndHostname   uintptr
	hwndAcctID     uintptr
	hwndAPIToken   uintptr
	hwndWorkerName uintptr
	hwndModels     uintptr
	hwndSave       uintptr
	hwndCancel     uintptr
	hwndShowKey    uintptr
	models         []config.ModelMapping
	layout         []layoutControl
	scrollPos      int32
	contentHeight  int32
}

var (
	settingsState     *settingsDialogState
	settingsClassAtom uint16
	settingsRegOnce   sync.Once
	settingsShowing   bool
	hSmallIcon        uintptr
	hBigIcon          uintptr
)

// ensureSettingsClass registers the settings window class once.
func ensureSettingsClass(iconBytes []byte) {
	settingsRegOnce.Do(func() {
		wndProc := syscall.NewCallback(settingsWindowProc)
		className, _ := syscall.UTF16PtrFromString("ZApiSettingsDlg")
		cursor, _, _ := pLoadCursorW.Call(0, idcArrow)

		wc := wndClassEx{
			Size:       uint32(unsafe.Sizeof(wndClassEx{})),
			WndProc:    wndProc,
			Background: colorBtnFace + 1,
			ClassName:  className,
			Cursor:     cursor,
		}
		atom, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		settingsClassAtom = uint16(atom)

		// Load the app icon from the embedded bytes for the window title bar.
		if iconBytes != nil {
			loadAppIcon(iconBytes)
		}
	})
}

// loadAppIcon loads the icon from bytes into both small (16x16) and big (32x32)
// handles using CreateIconFromResourceEx via user32.
var (
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
)

const (
	rtIcon = 3
)

func loadAppIcon(iconBytes []byte) {
	// Try to load via LoadImage from a temp file approach.
	// Write icon to temp, load with LoadImage, then clean up.
	tmpPath := os.TempDir() + "\\z-api-proxy-titlebar.ico"
	if err := os.WriteFile(tmpPath, iconBytes, 0600); err != nil {
		return
	}
	iconPath, _ := syscall.UTF16PtrFromString(tmpPath)

	// Small icon (16x16) for title bar.
	h, _, _ := pLoadImageW.Call(
		0, uintptr(unsafe.Pointer(iconPath)),
		uintptr(imageIcon), 16, 16, 0x10, // LR_LOADFROMFILE
	)
	if h != 0 {
		hSmallIcon = h
	}

	// Big icon (32x32) for taskbar/alt-tab.
	h2, _, _ := pLoadImageW.Call(
		0, uintptr(unsafe.Pointer(iconPath)),
		uintptr(imageIcon), 32, 32, 0x10,
	)
	if h2 != 0 {
		hBigIcon = h2
	}
}

func settingsWindowProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		ctrlID := wParam & 0xFFFF
		notif := wParam >> 16
		if notif == 0 { // BN_CLICKED
			switch ctrlID {
			case idSave:
				saveSettings(hwnd)
				pDestroyWindow.Call(hwnd)
			case idCancel:
				pDestroyWindow.Call(hwnd)
			case idShowKey:
				togglePasswordVisibility()
			case idModelAdd:
				addModelEntry(settingsState.hwndModels)
			case idModelRemove:
				removeModelEntry(settingsState.hwndModels)
			}
		}

	case wmSize:
		if settingsState != nil {
			relayoutControls()
		}
		pInvalidateRect.Call(hwnd, 0, 1)

	case wmMouseWheel:
		if settingsState != nil {
			wheel := int32(int16(wParam >> 16))
			scrollDelta := -wheel * 2 // lines to scroll
			newPos := settingsState.scrollPos + scrollDelta
			clientRect := struct{ Left, Top, Right, Bottom int32 }{}
			pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&clientRect)))
			clientH := clientRect.Bottom - clientRect.Top
			maxScroll := settingsState.contentHeight - clientH
			if maxScroll < 0 {
				maxScroll = 0
			}
			if newPos < 0 {
				newPos = 0
			}
			if newPos > maxScroll {
				newPos = maxScroll
			}
			if newPos != settingsState.scrollPos {
				oldPos := settingsState.scrollPos
				settingsState.scrollPos = newPos
				scrollAllChildren(int32(oldPos - newPos))
				pUpdateScrollbar(hwnd)
			}
		}

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	ret, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

// togglePasswordVisibility toggles ES_PASSWORD on the API key edit control.
func togglePasswordVisibility() {
	if settingsState == nil || settingsState.hwndAPIKey == 0 {
		return
	}
	// Read the checkbox state.
	checked, _, _ := pSendMessageW.Call(settingsState.hwndShowKey, bmGetCheck, 0, 0)
	style, _, _ := pGetWindowLong.Call(settingsState.hwndAPIKey, gwlStyle)
	if checked == bstChecked {
		// Remove password style to show the key.
		pSetWindowLong.Call(settingsState.hwndAPIKey, gwlStyle, style & ^uintptr(esPassword))
	} else {
		// Add password style to mask the key.
		pSetWindowLong.Call(settingsState.hwndAPIKey, gwlStyle, style | uintptr(esPassword))
	}
	// Force redraw.
	pInvalidateRect.Call(settingsState.hwndAPIKey, 0, 1)
}

// getControlText reads text from a Win32 edit control.
func getControlText(hwnd uintptr) string {
	length, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
	return syscall.UTF16ToString(buf)
}

// addModelEntry adds a "z.ai/model → model" entry to the models list.
func addModelEntry(hwndList uintptr) {
	if settingsState == nil {
		return
	}
	entry := config.ModelMapping{From: "z.ai/new-model", To: "new-model"}
	settingsState.models = append(settingsState.models, entry)
	refreshModelsList(hwndList)
}

// removeModelEntry removes the selected model entry from the list.
func removeModelEntry(hwndList uintptr) {
	if settingsState == nil {
		return
	}
	sel, _, _ := pSendMessageW.Call(hwndList, lbGetCurSel, 0, 0)
	if sel == ^uintptr(0) {
		return
	}
	idx := int(sel)
	if idx < 0 || idx >= len(settingsState.models) {
		return
	}
	settingsState.models = append(settingsState.models[:idx], settingsState.models[idx+1:]...)
	refreshModelsList(hwndList)
}

func refreshModelsList(hwndList uintptr) {
	count, _, _ := pSendMessageW.Call(hwndList, lbGetCount, 0, 0)
	for i := uintptr(0); i < count; i++ {
		pSendMessageW.Call(hwndList, lbDeleteString, 0, 0)
	}
	for _, m := range settingsState.models {
		label, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s → %s", m.From, m.To))
		pSendMessageW.Call(hwndList, lbAddString, 0, uintptr(unsafe.Pointer(label)))
	}
}

// relayoutControls repositions all controls when the window is resized.
// Fields keep their original Y positions and fixed heights. Only the width
// stretches to fill the window. The models listbox and bottom buttons anchor.
func relayoutControls() {
	if settingsState == nil {
		return
	}
	s := settingsState
	if s.hwnd == 0 {
		return
	}

	// Get current client area width.
	rect := struct{ Left, Top, Right, Bottom int32 }{}
	pGetClientRect.Call(s.hwnd, uintptr(unsafe.Pointer(&rect)))
	clientW := uintptr(rect.Right - rect.Left)

	mx := uintptr(20)
	lw := uintptr(130)
	gap := uintptr(8)
	newFW := clientW - mx - mx - lw - gap - gap // field width

	type move struct {
		hwnd     uintptr
		x, y, w, h uintptr
	}

	var moves []move

	// For each control stored with original Y, reposition X/W.
	// We stored Y positions in the original creation; here we just
	// re-set widths for the stretch fields.
	for _, lc := range s.layout {
		if lc.hwnd == 0 {
			continue
		}

		// Get current position.
		r := struct{ Left, Top, Right, Bottom int32 }{}
		pGetWindowRect.Call(lc.hwnd, uintptr(unsafe.Pointer(&r)))
		// Convert screen to client coords.
		pt := struct{ X, Y int32 }{r.Left, r.Top}
		pScreenToClient.Call(s.hwnd, uintptr(unsafe.Pointer(&pt)))
		curX := uintptr(pt.X)
		curY := uintptr(pt.Y)
		curW := uintptr(r.Right - r.Left)
		curH := uintptr(r.Bottom - r.Top)

		newW := curW
		newX := curX

		if lc.flags&lfStretch != 0 {
			newX = mx + lw + gap
			newW = newFW
			if lc.hwnd == s.hwndAPIKey {
				newW = newFW - 90
			}
		}
		if lc.flags&lfFullWidth != 0 {
			newX = mx
			newW = clientW - mx - mx
		}

		moves = append(moves, move{lc.hwnd, newX, curY, newW, curH})
	}

	for _, m := range moves {
		pMoveWindow.Call(m.hwnd, uintptr(m.x), uintptr(m.y), uintptr(m.w), uintptr(m.h), 1)
	}
}

// saveSettings reads all control values and writes the config to disk.
func saveSettings(hwnd uintptr) {
	if settingsState == nil {
		return
	}
	s := settingsState

	cfg := &config.Config{
		Server: config.ServerConfig{
			Listen: getControlText(s.hwndListen),
		},
		Upstream: config.UpstreamConfig{
			BaseURL: getControlText(s.hwndBaseURL),
			APIKey:  getControlText(s.hwndAPIKey),
		},
		Tunnel: config.TunnelConfig{
			Token:    getControlText(s.hwndToken),
			Hostname: getControlText(s.hwndHostname),
		},
		Cloudflare: config.CloudflareConfig{
			AccountID:  getControlText(s.hwndAcctID),
			APIToken:   getControlText(s.hwndAPIToken),
			WorkerName: getControlText(s.hwndWorkerName),
		},
		Models: s.models,
	}

	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8787"
	}
	if cfg.Upstream.BaseURL == "" {
		cfg.Upstream.BaseURL = "https://api.z.ai/api/coding/paas/v4"
	}

	verifyChk, _, _ := pSendMessageW.Call(s.hwndVerify, bmGetCheck, 0, 0)
	cfg.Security.VerifyKey = verifyChk == bstChecked

	namedChk, _, _ := pSendMessageW.Call(s.hwndNamed, bmGetCheck, 0, 0)
	if namedChk == bstChecked {
		cfg.Tunnel.Mode = "named"
	} else {
		cfg.Tunnel.Mode = "quick"
	}

	apiKey := getControlText(s.hwndAPIKey)
	cloudflareToken := getControlText(s.hwndAPIToken)
	tunnelToken := getControlText(s.hwndToken)

	data, err := toml.Marshal(cfg)
	if err != nil {
		messageBox("Failed to serialize config: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	if err := os.WriteFile(s.configPath, data, 0600); err != nil {
		messageBox("Failed to write config: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	secretsPath := config.DefaultSecretsPath()
	sec := struct {
		Upstream struct {
			APIKey string `toml:"api_key"`
		} `toml:"upstream"`
		Tunnel struct {
			Token string `toml:"token"`
		} `toml:"tunnel"`
		Cloudflare struct {
			APIToken string `toml:"api_token"`
		} `toml:"cloudflare"`
	}{}
	sec.Upstream.APIKey = apiKey
	sec.Tunnel.Token = tunnelToken
	sec.Cloudflare.APIToken = cloudflareToken

	secData, err := toml.Marshal(sec)
	if err != nil {
		messageBox("Failed to serialize secrets: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}
	header := []byte("# Z-API Proxy Secrets — auto-generated by settings dialog.\n# This file contains sensitive values. Keep it private!\n\n")
	if err := os.WriteFile(secretsPath, append(header, secData...), 0600); err != nil {
		messageBox("Failed to write secrets: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	log.Printf("settings: saved config to %s, secrets to %s", s.configPath, secretsPath)
}

// showSettingsDialog creates and runs the settings form.
func showSettingsDialog(cfg *config.Config, configPath string, iconBytes []byte) {
	winGuardMu.Lock()
	if winShowing {
		winGuardMu.Unlock()
		return
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

	ensureSettingsClass(iconBytes)

	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	winW, winH := 560, 760
	x := int32((int(sw) - winW) / 2)
	y := int32((int(sh) - winH) / 2)

	// WS_EX_CONTROLPARENT enables IsDialogMessage tab navigation.
	const wsExControlParent = 0x00010000
	title, _ := syscall.UTF16PtrFromString("Z-API Proxy — Settings")
	hwnd, _, _ := pCreateWindowExW.Call(
		wsExControlParent, uintptr(settingsClassAtom), uintptr(unsafe.Pointer(title)),
		uintptr(wsCaption|wsSysMenu|wsThickframe|wsMinimizeBox|wsMaximizeBox|wsVscroll),
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return
	}

	// Set the window icon.
	if hBigIcon != 0 {
		pSendMessageW.Call(hwnd, wmSetIcon, 1, hBigIcon) // ICON_BIG
	}
	if hSmallIcon != 0 {
		pSendMessageW.Call(hwnd, wmSetIcon, 0, hSmallIcon) // ICON_SMALL
	}

	mx := uintptr(20)
	lw := uintptr(130)
	fw := uintptr(370)
	ch := uintptr(22)
	_ = ch
	gap := uintptr(8)
	yPos := uintptr(12)

	addLayout := func(hwnd uintptr, flags layoutFlags) {
		settingsState.layout = append(settingsState.layout, layoutControl{hwnd: hwnd, flags: flags})
	}

	createLabel := func(text string, x, y, w, h uintptr) uintptr {
		staticClass, _ := syscall.UTF16PtrFromString("Static")
		label, _ := syscall.UTF16PtrFromString(text)
		lh, _, _ := pCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(staticClass)), uintptr(unsafe.Pointer(label)),
			uintptr(wsChild|wsVisible),
			uintptr(x), uintptr(y), uintptr(w), uintptr(h),
			hwnd, 0, 0, 0,
		)
		pSendMessageW.Call(lh, wmSetFont, fontHandle, 1)
		return lh
	}

	createEdit := func(id int, text string, x, y, w, h uintptr, password bool) uintptr {
		editClass, _ := syscall.UTF16PtrFromString("Edit")
		val, _ := syscall.UTF16PtrFromString(text)
		style := wsChild | wsVisible | esAutohscroll | wsBorder | wsTabstop
		if password {
			style |= esPassword
		}
		eh, _, _ := pCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(editClass)), uintptr(unsafe.Pointer(val)),
			uintptr(style),
			uintptr(x), uintptr(y), uintptr(w), uintptr(h),
			hwnd, uintptr(id), 0, 0,
		)
		pSendMessageW.Call(eh, wmSetFont, fontHandle, 1)
		return eh
	}

	createSection := func(title string, y *uintptr) {
		label, _ := syscall.UTF16PtrFromString(title)
		sh2, _, _ := pCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Static"))), uintptr(unsafe.Pointer(label)),
			uintptr(wsChild|wsVisible),
			uintptr(mx), uintptr(*y), uintptr(fw+lw), uintptr(ch),
			hwnd, 0, 0, 0,
		)
		pSendMessageW.Call(sh2, wmSetFont, fontHandle, 1)
		*y += ch + gap
	}

	btnClass, _ := syscall.UTF16PtrFromString("Button")

	// Initialize state now so addLayout works.
	settingsState = &settingsDialogState{
		hwnd:       hwnd,
		configPath: configPath,
		cfg:        cfg,
	}

	// ── Server section ──
	createSection("Server", &yPos)
	createLabel("Listen Address:", mx, yPos, lw, ch)
	hwndListen := createEdit(idListenEd, cfg.Server.Listen, mx+lw+gap, yPos, fw, ch, false)
	addLayout(hwndListen, lfStretch)
	yPos += ch + gap

	// ── Upstream section ──
	yPos += gap
	createSection("Upstream", &yPos)
	createLabel("Base URL:", mx, yPos, lw, ch)
	hwndBaseURL := createEdit(idBaseURLEd, cfg.Upstream.BaseURL, mx+lw+gap, yPos, fw, ch, false)
	addLayout(hwndBaseURL, lfStretch)
	yPos += ch + gap
	createLabel("API Key:", mx, yPos, lw, ch)
	hwndAPIKey := createEdit(idAPIKeyEd, cfg.Upstream.APIKey, mx+lw+gap, yPos, fw-90, ch, true)
	addLayout(hwndAPIKey, lfStretch)
	showKeyLabel, _ := syscall.UTF16PtrFromString("Show")
	hwndShowKey, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(showKeyLabel)),
		uintptr(wsChild|wsVisible|bsAutocheckbox),
		uintptr(mx+lw+gap+fw-80), uintptr(yPos), 80, ch,
		hwnd, idShowKey, 0, 0,
	)
	pSendMessageW.Call(hwndShowKey, wmSetFont, fontHandle, 1)
	yPos += ch + gap

	// ── Security section ──
	yPos += gap
	createSection("Security", &yPos)
	verifyLabel, _ := syscall.UTF16PtrFromString("Verify API Key — always enabled (required for security)")
	verifyStyle := wsChild | wsVisible | bsAutocheckbox | wsDisabled
	hwndVerify, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(verifyLabel)),
		uintptr(verifyStyle),
		uintptr(mx), uintptr(yPos), uintptr(fw+lw), uintptr(ch),
		hwnd, idVerifyChk, 0, 0,
	)
	pSendMessageW.Call(hwndVerify, wmSetFont, fontHandle, 1)
	pSendMessageW.Call(hwndVerify, bmSetCheck, bstChecked, 0)
	yPos += ch + gap

	// ── Tunnel section ──
	yPos += gap
	createSection("Tunnel", &yPos)
	isNamed := cfg.Tunnel.Mode == "named"
	quickLabel, _ := syscall.UTF16PtrFromString("Quick (ephemeral URL)")
	quickStyle := wsChild | wsVisible | bsAutoradioButton | wsGroup | wsTabstop
	hwndQuick, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(quickLabel)),
		uintptr(quickStyle),
		uintptr(mx+lw+gap), uintptr(yPos), 200, ch,
		hwnd, idTunnelQuick, 0, 0,
	)
	pSendMessageW.Call(hwndQuick, wmSetFont, fontHandle, 1)
	if !isNamed {
		pSendMessageW.Call(hwndQuick, bmSetCheck, bstChecked, 0)
	}
	yPos += ch + gap
	namedLabel, _ := syscall.UTF16PtrFromString("Named (stable URL)")
	namedStyle := wsChild | wsVisible | bsAutoradioButton | wsTabstop
	hwndNamed, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(namedLabel)),
		uintptr(namedStyle),
		uintptr(mx+lw+gap), uintptr(yPos), 200, ch,
		hwnd, idTunnelNamed, 0, 0,
	)
	pSendMessageW.Call(hwndNamed, wmSetFont, fontHandle, 1)
	if isNamed {
		pSendMessageW.Call(hwndNamed, bmSetCheck, bstChecked, 0)
	}
	yPos += ch + gap
	createLabel("Token:", mx, yPos, lw, ch)
	hwndToken := createEdit(idTokenEd, cfg.Tunnel.Token, mx+lw+gap, yPos, fw, ch, true)
	// WS_GROUP on this edit closes the radio button group above.
	style, _, _ := pGetWindowLong.Call(hwndToken, gwlStyle)
	pSetWindowLong.Call(hwndToken, gwlStyle, style|wsGroup|wsTabstop)
	addLayout(hwndToken, lfStretch)
	yPos += ch + gap
	createLabel("Hostname:", mx, yPos, lw, ch)
	hwndHostname := createEdit(idHostnameEd, cfg.Tunnel.Hostname, mx+lw+gap, yPos, fw, ch, false)
	addLayout(hwndHostname, lfStretch)
	yPos += ch + gap

	// ── Cloudflare Worker section ──
	yPos += gap
	createSection("Cloudflare Worker", &yPos)
	createLabel("Account ID:", mx, yPos, lw, ch)
	hwndAcctID := createEdit(idAcctIDEd, cfg.Cloudflare.AccountID, mx+lw+gap, yPos, fw, ch, false)
	addLayout(hwndAcctID, lfStretch)
	yPos += ch + gap
	createLabel("API Token:", mx, yPos, lw, ch)
	hwndAPIToken := createEdit(idAPITokenEd, cfg.Cloudflare.APIToken, mx+lw+gap, yPos, fw, ch, true)
	addLayout(hwndAPIToken, lfStretch)
	yPos += ch + gap
	createLabel("Worker Name:", mx, yPos, lw, ch)
	workerName := cfg.Cloudflare.WorkerName
	if workerName == "" {
		workerName = "z-api-proxy"
	}
	hwndWorkerName := createEdit(idWorkerNameEd, workerName, mx+lw+gap, yPos, fw, ch, false)
	addLayout(hwndWorkerName, lfStretch)
	yPos += ch + gap

	// ── Model Mappings section ──
	yPos += gap
	createSection("Model Mappings", &yPos)
	listClass, _ := syscall.UTF16PtrFromString("ListBox")
	hwndModels, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(listClass)), 0,
		uintptr(wsChild|wsVisible|lbsNotify|wsVscroll|wsBorder),
		uintptr(mx), uintptr(yPos), uintptr(fw+lw), 120,
		hwnd, idModelsList, 0, 0,
	)
	pSendMessageW.Call(hwndModels, wmSetFont, fontHandle, 1)
	addLayout(hwndModels, lfFullWidth)
	yPos += 125

	addLabel, _ := syscall.UTF16PtrFromString("Add")
	_, _, _ = pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(addLabel)),
		uintptr(wsChild|wsVisible),
		uintptr(mx), uintptr(yPos), 80, ch,
		hwnd, idModelAdd, 0, 0,
	)
	pSendMessageW.Call(hwndModels, wmSetFont, fontHandle, 1)
	removeLabel, _ := syscall.UTF16PtrFromString("Remove")
	_, _, _ = pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(removeLabel)),
		uintptr(wsChild|wsVisible),
		uintptr(mx+90), uintptr(yPos), 80, ch,
		hwnd, idModelRemove, 0, 0,
	)
	yPos += ch + gap*2

	// ── Buttons ──
	saveLabel, _ := syscall.UTF16PtrFromString("Save")
	hwndSave, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(saveLabel)),
		uintptr(wsChild|wsVisible|bsCenter),
		uintptr(mx+fw+lw-200), uintptr(yPos), 90, 30,
		hwnd, idSave, 0, 0,
	)
	pSendMessageW.Call(hwndSave, wmSetFont, fontHandle, 1)

	cancelLabel, _ := syscall.UTF16PtrFromString("Cancel")
	hwndCancel, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(cancelLabel)),
		uintptr(wsChild|wsVisible|bsCenter),
		uintptr(mx+fw+lw-100), uintptr(yPos), 90, 30,
		hwnd, idCancel, 0, 0,
	)
	pSendMessageW.Call(hwndCancel, wmSetFont, fontHandle, 1)

	// Store all handles in state.
	modelsCopy := make([]config.ModelMapping, len(cfg.Models))
	copy(modelsCopy, cfg.Models)
	settingsState.hwndListen = hwndListen
	settingsState.hwndBaseURL = hwndBaseURL
	settingsState.hwndAPIKey = hwndAPIKey
	settingsState.hwndVerify = hwndVerify
	settingsState.hwndQuick = hwndQuick
	settingsState.hwndNamed = hwndNamed
	settingsState.hwndToken = hwndToken
	settingsState.hwndHostname = hwndHostname
	settingsState.hwndAcctID = hwndAcctID
	settingsState.hwndAPIToken = hwndAPIToken
	settingsState.hwndWorkerName = hwndWorkerName
	settingsState.hwndModels = hwndModels
	settingsState.hwndSave = hwndSave
	settingsState.hwndCancel = hwndCancel
	settingsState.hwndShowKey = hwndShowKey
	settingsState.models = modelsCopy

	// Record total content height for scrollbar calculations.
	settingsState.contentHeight = int32(yPos) + int32(gap) + 30 + int32(gap)

	refreshModelsList(hwndModels)

	// Apply system dark/light theme to all controls.
	applyTheme(hwnd, isSystemDarkMode())

	pShowWindow.Call(hwnd, swShow)

	var m winMsg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break
		}
		// IsDialogMessage handles Tab/Shift+Tab, arrow keys on radio
		// buttons, and Enter on the default button. Without it, child
		// controls are non-interactive.
		processed, _, _ := pIsDialogMessageW.Call(hwnd, uintptr(unsafe.Pointer(&m)))
		if processed != 0 {
			continue
		}
		// TranslateMessage converts WM_KEYDOWN to WM_CHAR so edit
		// controls can process typed characters.
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// scrollAllChildren shifts all child windows by dy pixels vertically.
func scrollAllChildren(dy int32) {
	if settingsState == nil {
		return
	}
	s := settingsState
	allHwnds := []uintptr{
		s.hwndListen, s.hwndBaseURL, s.hwndAPIKey, s.hwndVerify,
		s.hwndQuick, s.hwndNamed, s.hwndToken, s.hwndHostname,
		s.hwndAcctID, s.hwndAPIToken, s.hwndWorkerName, s.hwndModels,
		s.hwndSave, s.hwndCancel, s.hwndShowKey,
	}
	for _, hwnd := range s.layout {
		allHwnds = append(allHwnds, hwnd.hwnd)
	}
	for _, hwnd := range allHwnds {
		if hwnd == 0 {
			continue
		}
		r := struct{ Left, Top, Right, Bottom int32 }{}
		pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		pt := struct{ X, Y int32 }{r.Left, r.Top}
		pScreenToClient.Call(s.hwnd, uintptr(unsafe.Pointer(&pt)))
		curH := r.Bottom - r.Top
		curW := r.Right - r.Left
		newY := pt.Y + dy
		pMoveWindow.Call(hwnd, uintptr(pt.X), uintptr(newY), uintptr(curW), uintptr(curH), 1)
	}
}

// pUpdateScrollbar updates the scrollbar info for the window.
func pUpdateScrollbar(hwnd uintptr) {
	if settingsState == nil {
		return
	}
	si := struct {
		Size       uint32
		Mask       uint32
		Min        int32
		Max        int32
		Page       uint32
		Pos        int32
		TrackPos   int32
	}{}
	si.Size = uint32(unsafe.Sizeof(si))
	si.Mask = 0x4 | 0x1 | 0x2 | 0x10 // SIF_RANGE | SIF_POS | SIF_PAGE | SIF_DISABLENOSCROLL
	si.Min = 0
	si.Max = settingsState.contentHeight
	clientRect := struct{ Left, Top, Right, Bottom int32 }{}
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&clientRect)))
	si.Page = uint32(clientRect.Bottom - clientRect.Top)
	si.Pos = settingsState.scrollPos

	pSetScrollInfo.Call(hwnd, sbVert, uintptr(unsafe.Pointer(&si)), 1)
}

// Additional Win32 procs for scroll.
var (
	pGetClientRect  = user32.NewProc("GetClientRect")
	pGetWindowRect  = user32.NewProc("GetWindowRect")
	pScreenToClient = user32.NewProc("ScreenToClient")
	pSetScrollInfo  = user32.NewProc("SetScrollInfo")
)

// keep imports alive
var _ = strings.TrimSpace
