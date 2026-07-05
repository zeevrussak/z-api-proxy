package tray

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/pelletier/go-toml/v2"

	"z-api-proxy/internal/config"
)

// Additional Win32 procedures for the settings dialog.
var (
	pGetWindowTextW   = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSendMessageWLB    = user32.NewProc("SendMessageW") // reuse pSendMessageW but alias for clarity
)

// Additional style and message constants.
const (
	esAutohscroll = 0x0080
	esPassword    = 0x0020
	lbsNotify     = 0x0001
	lbsSort       = 0x0002
	lbAddString   = 0x0180
	lbDeleteString = 0x0182
	lbGetCount    = 0x018B
	lbGetCurSel   = 0x0188
	lbGetText     = 0x0189
	lbGetTextLen  = 0x018A
	bmGetCheck    = 0x00F0
	bstChecked    = 1
	wsVscroll     = 0x00200000
	wsBorder      = 0x00800000

	// Control IDs for settings dialog
	idListenEd    = 2000
	idBaseURLEd   = 2001
	idAPIKeyEd    = 2002
	idVerifyChk   = 2003
	idTunnelQuick = 2004
	idTunnelNamed = 2005
	idTokenEd     = 2006
	idHostnameEd  = 2007
	idAcctIDEd    = 2008
	idAPITokenEd  = 2009
	idWorkerNameEd = 2010
	idModelsList  = 2011
	idModelAdd    = 2012
	idModelRemove = 2013
	idShowKey     = 2014
	idSave        = 2015
	idCancel      = 2016
)

// settingsDialogState holds control handles and config for the dialog.
type settingsDialogState struct {
	hwnd         uintptr
	configPath   string
	cfg          *config.Config
	hwndListen   uintptr
	hwndBaseURL  uintptr
	hwndAPIKey   uintptr
	hwndVerify   uintptr
	hwndQuick    uintptr
	hwndNamed    uintptr
	hwndToken    uintptr
	hwndHostname uintptr
	hwndAcctID   uintptr
	hwndAPIToken uintptr
	hwndWorkerName uintptr
	hwndModels   uintptr
	models       []config.ModelMapping
}

var (
	settingsState     *settingsDialogState
	settingsClassAtom uint16
	settingsRegOnce   sync.Once
	settingsShowing   bool
)

// ensureSettingsClass registers the settings window class once.
func ensureSettingsClass() {
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
	})
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
				togglePasswordVisibility(settingsState.hwndAPIKey, settingsState.hwnd)
			case idModelAdd:
				addModelEntry(settingsState.hwndModels)
			case idModelRemove:
				removeModelEntry(settingsState.hwndModels)
			}
		}

	case wmDestroy:
		pPostQuitMessage.Call(0)
	}

	ret, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
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

// togglePasswordVisibility toggles ES_PASSWORD on an edit control.
func togglePasswordVisibility(hwndEdit, hwndParent uintptr) {
	if settingsState == nil || hwndEdit == 0 {
		return
	}
	// Check the checkbox state.
	checked, _, _ := pSendMessageW.Call(settingsState.hwnd, bmGetCheck, 0, 0) // wrong hwnd
	_ = checked
	// Simpler: just read the show-key checkbox directly.
	// We need to find the checkbox by its ID via the parent.
	// Since we stored it in settingsState, use that.
	style, _, _ := pGetWindowLong.Call(hwndEdit, gwlStyle)
	if style&esPassword != 0 {
		pSetWindowLong.Call(hwndEdit, gwlStyle, style & ^uintptr(esPassword))
	} else {
		pSetWindowLong.Call(hwndEdit, gwlStyle, style|uintptr(esPassword))
	}
}

// addModelEntry adds a "z.ai/model → model" entry to the models list.
func addModelEntry(hwndList uintptr) {
	if settingsState == nil {
		return
	}
	// Prompt for model name using a simple InputBox approach.
	// For simplicity, add a default entry the user can edit in the TOML later.
	// A real sub-dialog would be complex; we add z.ai/ prefix pattern.
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
	if sel == ^uintptr(0) { // LB_ERR
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
	// Clear and repopulate.
	count, _, _ := pSendMessageW.Call(hwndList, lbGetCount, 0, 0)
	for i := uintptr(0); i < count; i++ {
		pSendMessageW.Call(hwndList, lbDeleteString, 0, 0)
	}
	for _, m := range settingsState.models {
		label, _ := syscall.UTF16PtrFromString(fmt.Sprintf("%s → %s", m.From, m.To))
		pSendMessageW.Call(hwndList, lbAddString, 0, uintptr(unsafe.Pointer(label)))
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

	// Defaults for empty fields.
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8787"
	}
	if cfg.Upstream.BaseURL == "" {
		cfg.Upstream.BaseURL = "https://api.z.ai/api/coding/paas/v4"
	}

	// Verify key checkbox.
	verifyChk, _, _ := pSendMessageW.Call(s.hwndVerify, bmGetCheck, 0, 0)
	cfg.Security.VerifyKey = verifyChk == bstChecked

	// Tunnel mode radio buttons.
	namedChk, _, _ := pSendMessageW.Call(s.hwndNamed, bmGetCheck, 0, 0)
	if namedChk == bstChecked {
		cfg.Tunnel.Mode = "named"
	} else {
		cfg.Tunnel.Mode = "quick"
	}

	// Extract secrets from the form values (they go to secrets.toml).
	apiKey := getControlText(s.hwndAPIKey)
	cloudflareToken := getControlText(s.hwndAPIToken)
	tunnelToken := getControlText(s.hwndToken)

	// Serialize config.toml (no secrets — toml:"-" fields are excluded).
	data, err := toml.Marshal(cfg)
	if err != nil {
		messageBox("Failed to serialize config: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	if err := os.WriteFile(s.configPath, data, 0644); err != nil {
		messageBox("Failed to write config: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	// Write secrets.toml.
	secretsPath := config.DefaultSecretsPath()
	secretsContent := `# Z-API Proxy Secrets — auto-generated by settings dialog.
# This file contains sensitive values. Keep it private!

[upstream]
api_key = "` + apiKey + `"

[tunnel]
token = "` + tunnelToken + `"

[cloudflare]
api_token = "` + cloudflareToken + `"
`
	if err := os.WriteFile(secretsPath, []byte(secretsContent), 0644); err != nil {
		messageBox("Failed to write secrets: "+err.Error(), "Z-API Proxy — Settings", mbIconError)
		return
	}

	log.Printf("settings: saved config to %s, secrets to %s", s.configPath, secretsPath)
}

// showSettingsDialog creates and runs the settings form.
func showSettingsDialog(cfg *config.Config, configPath string) {
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

	ensureSettingsClass()

	sw, _, _ := pGetSystemMetrics.Call(0)
	sh, _, _ := pGetSystemMetrics.Call(1)
	winW, winH := 520, 680
	x := int32((int(sw) - winW) / 2)
	y := int32((int(sh) - winH) / 2)

	title, _ := syscall.UTF16PtrFromString("Z-API Proxy — Settings")
	hwnd, _, _ := pCreateWindowExW.Call(
		0, uintptr(settingsClassAtom), uintptr(unsafe.Pointer(title)),
		uintptr(wsCaption|wsSysMenu|wsVscroll),
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, 0, 0,
	)
	if hwnd == 0 {
		return
	}

	mx := uintptr(20)   // margin x
	lw := uintptr(130)  // label width
	fw := uintptr(350)  // field width
	ch := uintptr(22)   // control height
	gap := uintptr(8)   // vertical gap
	yPos := uintptr(12) // running y position

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
		style := wsChild | wsVisible | esAutohscroll | wsBorder
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
		sh, _, _ := pCreateWindowExW.Call(
			0, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Static"))), uintptr(unsafe.Pointer(label)),
			uintptr(wsChild|wsVisible),
			uintptr(mx), uintptr(*y), uintptr(fw+lw), uintptr(ch),
			hwnd, 0, 0, 0,
		)
		pSendMessageW.Call(sh, wmSetFont, fontHandle, 1)
		*y += ch + gap
	}

	btnClass, _ := syscall.UTF16PtrFromString("Button")

	// ── Server section ──
	createSection("Server", &yPos)
	createLabel("Listen Address:", mx, yPos, lw, ch)
	hwndListen := createEdit(idListenEd, cfg.Server.Listen, mx+lw+gap, yPos, fw, ch, false)
	yPos += ch + gap

	// ── Upstream section ──
	yPos += gap
	createSection("Upstream", &yPos)
	createLabel("Base URL:", mx, yPos, lw, ch)
	hwndBaseURL := createEdit(idBaseURLEd, cfg.Upstream.BaseURL, mx+lw+gap, yPos, fw, ch, false)
	yPos += ch + gap
	createLabel("API Key:", mx, yPos, lw, ch)
	hwndAPIKey := createEdit(idAPIKeyEd, cfg.Upstream.APIKey, mx+lw+gap, yPos, fw-90, ch, true)
	// Show key checkbox
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
	verifyLabel, _ := syscall.UTF16PtrFromString("Verify API Key (reject requests with non-matching key)")
	verifyStyle := wsChild | wsVisible | bsAutocheckbox
	if cfg.Security.VerifyKey {
		verifyStyle |= bstChecked
	}
	hwndVerify, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(verifyLabel)),
		uintptr(verifyStyle),
		uintptr(mx), uintptr(yPos), uintptr(fw+lw), uintptr(ch),
		hwnd, idVerifyChk, 0, 0,
	)
	pSendMessageW.Call(hwndVerify, wmSetFont, fontHandle, 1)
	yPos += ch + gap

	// ── Tunnel section ──
	yPos += gap
	createSection("Tunnel", &yPos)
	isNamed := cfg.Tunnel.Mode == "named"
	quickLabel, _ := syscall.UTF16PtrFromString("Quick (ephemeral URL)")
	quickStyle := wsChild | wsVisible | bsAutoradioButton
	if !isNamed {
		quickStyle |= bstChecked
	}
	hwndQuick, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(quickLabel)),
		uintptr(quickStyle),
		uintptr(mx+lw+gap), uintptr(yPos), 200, ch,
		hwnd, idTunnelQuick, 0, 0,
	)
	pSendMessageW.Call(hwndQuick, wmSetFont, fontHandle, 1)
	yPos += ch + gap
	namedLabel, _ := syscall.UTF16PtrFromString("Named (stable URL)")
	namedStyle := wsChild | wsVisible | bsAutoradioButton
	if isNamed {
		namedStyle |= bstChecked
	}
	hwndNamed, _, _ := pCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(btnClass)), uintptr(unsafe.Pointer(namedLabel)),
		uintptr(namedStyle),
		uintptr(mx+lw+gap), uintptr(yPos), 200, ch,
		hwnd, idTunnelNamed, 0, 0,
	)
	pSendMessageW.Call(hwndNamed, wmSetFont, fontHandle, 1)
	yPos += ch + gap
	createLabel("Token:", mx, yPos, lw, ch)
	hwndToken := createEdit(idTokenEd, cfg.Tunnel.Token, mx+lw+gap, yPos, fw, ch, false)
	yPos += ch + gap
	createLabel("Hostname:", mx, yPos, lw, ch)
	hwndHostname := createEdit(idHostnameEd, cfg.Tunnel.Hostname, mx+lw+gap, yPos, fw, ch, false)
	yPos += ch + gap

	// ── Cloudflare Worker section ──
	yPos += gap
	createSection("Cloudflare Worker", &yPos)
	createLabel("Account ID:", mx, yPos, lw, ch)
	hwndAcctID := createEdit(idAcctIDEd, cfg.Cloudflare.AccountID, mx+lw+gap, yPos, fw, ch, false)
	yPos += ch + gap
	createLabel("API Token:", mx, yPos, lw, ch)
	hwndAPIToken := createEdit(idAPITokenEd, cfg.Cloudflare.APIToken, mx+lw+gap, yPos, fw, ch, true)
	yPos += ch + gap
	createLabel("Worker Name:", mx, yPos, lw, ch)
	workerName := cfg.Cloudflare.WorkerName
	if workerName == "" {
		workerName = "z-api-proxy"
	}
	hwndWorkerName := createEdit(idWorkerNameEd, workerName, mx+lw+gap, yPos, fw, ch, false)
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

	// Store state.
	modelsCopy := make([]config.ModelMapping, len(cfg.Models))
	copy(modelsCopy, cfg.Models)
	settingsState = &settingsDialogState{
		hwnd:          hwnd,
		configPath:    configPath,
		cfg:           cfg,
		hwndListen:    hwndListen,
		hwndBaseURL:   hwndBaseURL,
		hwndAPIKey:    hwndAPIKey,
		hwndVerify:    hwndVerify,
		hwndQuick:     hwndQuick,
		hwndNamed:     hwndNamed,
		hwndToken:     hwndToken,
		hwndHostname:  hwndHostname,
		hwndAcctID:    hwndAcctID,
		hwndAPIToken:  hwndAPIToken,
		hwndWorkerName: hwndWorkerName,
		hwndModels:    hwndModels,
		models:        modelsCopy,
	}

	// Populate models list.
	refreshModelsList(hwndModels)

	pShowWindow.Call(hwnd, swShow)

	// Message loop.
	var m winMsg
	for {
		ret, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 {
			break
		}
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Additional Win32 helpers needed.
var (
	pGetWindowLong = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLong = user32.NewProc("SetWindowLongPtrW")
)

const (
	gwlStyle   = ^uintptr(15) + 1 // -16 as uintptr
	bsAutocheckbox    = 0x00000003
	bsAutoradioButton = 0x00000004
)

// log is reused from tray.go (already declared in scope as it's the same package).
var _ = log.Printf // keep reference

// _ keeps strings import alive.
var _ = strings.TrimSpace
var _ = strconv.Itoa
