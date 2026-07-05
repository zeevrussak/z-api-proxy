package tray

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// theming provides dark/light mode detection and application for Win32 windows.

var (
	uxtheme          = syscall.NewLazyDLL("uxtheme.dll")
	pSetWindowTheme  = uxtheme.NewProc("SetWindowTheme")
	pIsDarkModeAllowedForApp = uxtheme.NewProc("AllowDarkModeForApp")
	pOpenNcTheme     = syscall.NewLazyDLL("user32.dll").NewProc("OpenThemeData")
)

const (
	darkModeExplorer = "DarkMode_Explorer"
	explorer         = "Explorer"
)

// isSystemDarkMode checks if Windows is in dark mode.
func isSystemDarkMode() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE)
	if err != nil {
		return false // default to light mode
	}
	defer k.Close()

	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false
	}
	return val == 0 // 0 = dark mode, 1 = light mode
}

// applyTheme applies the system theme to a window and its controls.
// In dark mode, sets the background to dark and text to light.
func applyTheme(hwnd uintptr, dark bool) {
	if dark {
		// Enable dark mode for the window title bar (Windows 10 1809+).
		// SetWindowTheme with "DarkMode_Explorer" on the window.
		applyThemeToControl(hwnd, darkModeExplorer)

		// Apply to child windows — they will be enumerated via the layout.
		if settingsState != nil {
			controls := []uintptr{
				settingsState.hwndListen, settingsState.hwndBaseURL,
				settingsState.hwndAPIKey, settingsState.hwndToken,
				settingsState.hwndHostname, settingsState.hwndAcctID,
				settingsState.hwndAPIToken, settingsState.hwndWorkerName,
				settingsState.hwndModels,
			}
			for _, c := range controls {
				if c != 0 {
					applyThemeToControl(c, darkModeExplorer)
				}
			}
		}
	}
}

// applyThemeToControl sets the visual theme on a single control.
func applyThemeToControl(hwnd uintptr, theme string) {
	themePtr, _ := syscall.UTF16PtrFromString(theme)
	pSetWindowTheme.Call(hwnd, uintptr(unsafe.Pointer(themePtr)), 0)
}
