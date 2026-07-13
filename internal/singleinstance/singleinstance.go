// Package singleinstance enforces one running z-api-proxy process per
// Windows session via a named kernel mutex.
package singleinstance

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// MutexName is the Local\ (per-session) kernel object name. Holding an
// open handle to this mutex is the ownership proof; CloseHandle (or
// process exit) releases it for the next launch.
const MutexName = `Local\Z-API-Proxy`

// ErrAlreadyRunning is returned when another process already holds MutexName.
var ErrAlreadyRunning = errors.New("another instance is already running")

// Lock is an acquired single-instance mutex. Call Release when the
// process is shutting down (defer is fine); the OS also releases it on
// process exit if Release is skipped.
type Lock struct {
	handle windows.Handle
}

// Acquire creates (or opens) the named mutex. On success the caller owns
// the sole instance for this session until Release. If another process
// already holds it, returns ErrAlreadyRunning.
func Acquire(name string) (*Lock, error) {
	if name == "" {
		name = MutexName
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("mutex name: %w", err)
	}
	// initialOwner=false: existence of the named object is the lock;
	// we only need the handle kept open for the process lifetime.
	handle, err := windows.CreateMutex(nil, false, ptr)
	if err == windows.ERROR_ALREADY_EXISTS {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		return nil, fmt.Errorf("CreateMutex: %w", err)
	}
	return &Lock{handle: handle}, nil
}

// Release closes the mutex handle, allowing another instance to start.
// Safe to call on a nil Lock or more than once.
func (l *Lock) Release() {
	if l == nil || l.handle == 0 {
		return
	}
	_ = windows.CloseHandle(l.handle)
	l.handle = 0
}

// NotifyAlreadyRunning shows a native MessageBox telling the user the
// app is already running. Used when Acquire returns ErrAlreadyRunning
// before the tray is up.
func NotifyAlreadyRunning() {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	text, err1 := syscall.UTF16PtrFromString("Z-API-Proxy is already running.")
	caption, err2 := syscall.UTF16PtrFromString("Z-API-Proxy")
	if err1 != nil || err2 != nil {
		return
	}
	const (
		mbOK       = 0x00000000
		mbIconInfo = 0x00000040
	)
	proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), mbOK|mbIconInfo)
}
