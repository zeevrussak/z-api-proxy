package tray

import (
	"sync"
	"testing"
	"time"
)

// TestMutexPreventsDoubleExecution verifies that TryLock prevents
// concurrent execution — the second call is rejected immediately.
// This is the mechanism that prevents duplicate process dialogs when
// a tray menu item fires its channel multiple times.
func TestMutexPreventsDoubleExecution(t *testing.T) {
	var mu sync.Mutex

	// Simulate first click acquiring the lock.
	if !mu.TryLock() {
		t.Fatal("first TryLock should succeed")
	}

	// Simulate second buffered click arriving while first is running.
	if mu.TryLock() {
		t.Fatal("second TryLock should fail while first holds lock")
	}

	// Release lock (operation completes).
	mu.Unlock()

	// Now a third click should succeed.
	if !mu.TryLock() {
		t.Fatal("TryLock should succeed after Unlock")
	}
	mu.Unlock()
}

// TestRegisterMutexIsSyncMutex verifies the package-level registerMutex
// behaves correctly for the guard pattern.
func TestRegisterMutexIsSyncMutex(t *testing.T) {
	// Verify it starts unlocked.
	if !registerMutex.TryLock() {
		t.Fatal("registerMutex should start unlocked")
	}

	// Verify re-entry fails.
	if registerMutex.TryLock() {
		t.Fatal("registerMutex double-lock should fail")
	}

	registerMutex.Unlock()

	// Verify it's unlocked again.
	if !registerMutex.TryLock() {
		t.Fatal("registerMutex should be unlocked after Unlock")
	}
	registerMutex.Unlock()
}

// TestDeployMutexIsSyncMutex verifies the package-level deployMutex.
func TestDeployMutexIsSyncMutex(t *testing.T) {
	if !deployMutex.TryLock() {
		t.Fatal("deployMutex should start unlocked")
	}
	if deployMutex.TryLock() {
		t.Fatal("deployMutex double-lock should fail")
	}
	deployMutex.Unlock()
	if !deployMutex.TryLock() {
		t.Fatal("deployMutex should be unlocked after Unlock")
	}
	deployMutex.Unlock()
}

// TestMutexSerializes verifies operations are serialized.
func TestMutexSerializes(t *testing.T) {
	var mu sync.Mutex
	order := make([]int, 0, 4)
	done := make(chan bool, 4)

	for i := 0; i < 4; i++ {
		go func(n int) {
			for {
				if mu.TryLock() {
					order = append(order, n)
					time.Sleep(10 * time.Millisecond)
					mu.Unlock()
					done <- true
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for goroutine")
		}
	}

	if len(order) != 4 {
		t.Errorf("expected 4 operations, got %d", len(order))
	}
}
