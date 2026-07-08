package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"z-api-proxy/internal/config"
	"z-api-proxy/internal/tray"
)

// testWindowCount builds the exe, runs --test-ui, and counts how many
// top-level windows exist when the process dialog should be open.
// Uses EnumWindows Win32 API.

var (
	pEnumWindows    = syscall.NewLazyDLL("user32.dll").NewProc("EnumWindows")
	pGetWindowTextW = syscall.NewLazyDLL("user32.dll").NewProc("GetWindowTextW")
	pIsWindowVisible = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")
)

var visibleWindows []string

func enumWindowsProc(hwnd uintptr, lparam uintptr) uintptr {
	visible, _, _ := pIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return 1 // continue
	}

	buf := make([]uint16, 256)
	ret, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	if ret > 0 {
		title := syscall.UTF16ToString(buf)
		if title != "" {
			visibleWindows = append(visibleWindows, title)
		}
	}
	return 1 // continue
}

func countVisibleWindows() []string {
	visibleWindows = nil
	enumProc := syscall.NewCallback(enumWindowsProc)
	pEnumWindows.Call(enumProc, 0)
	return visibleWindows
}

func testRegisterWindowCount() int {
	configPath := config.DefaultConfigPath()
	manager, err := config.NewManager(configPath)
	if err != nil {
		fmt.Printf("config error: %v\n", err)
		return -1
	}
	cfg := manager.Get()

	// Start register in a goroutine (simulates tray click).
	go func() {
		runtime.LockOSThread()
		tray.TestRegisterModels(cfg, configPath)
	}()

	// Wait for the dialog to appear.
	time.Sleep(2 * time.Second)

	// Count visible windows.
	windows := countVisibleWindows()
	zapiWindows := []string{}
	for _, w := range windows {
		// Only count our actual dialog windows, not File Explorer or other apps.
		if (contains(w, "Z-API Proxy —") || contains(w, "Registering")) && !contains(w, "File Explorer") {
			zapiWindows = append(zapiWindows, w)
		}
	}

	fmt.Printf("Visible z-api-proxy windows (%d):\n", len(zapiWindows))
	for _, w := range zapiWindows {
		fmt.Printf("  - %s\n", w)
	}
	fmt.Printf("Total visible windows: %d\n", len(windows))
	for _, w := range windows {
		fmt.Printf("  - %s\n", w)
	}

	return len(zapiWindows)
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--test-windows" {
		count := testRegisterWindowCount()
		fmt.Printf("\nResult: %d z-api-proxy windows open\n", count)
		if count > 1 {
			fmt.Println("FAIL: More than one window detected!")
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Normal app startup.
	logPath := filepath.Join(config.AppConfigDir(), "proxy.log")
	_ = logPath
	fmt.Println("Use --test-windows to test window count")
}
