package tray

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNoDuplicateWindowOnDoubleClick simulates the exact tray behavior:
// a menu channel fires twice rapidly. Verifies only one operation runs.
func TestNoDuplicateWindowOnDoubleClick(t *testing.T) {
	var mu sync.Mutex
	var execCount int32

	clickCh := make(chan struct{}, 4)

	// Simulate two rapid clicks.
	clickCh <- struct{}{}
	clickCh <- struct{}{}

	// Process clicks like the select loop does.
	for i := 0; i < 2; i++ {
		select {
		case <-clickCh:
			if mu.TryLock() {
				go func() {
					defer mu.Unlock()
					atomic.AddInt32(&execCount, 1)
					time.Sleep(100 * time.Millisecond)
				}()
			}
		default:
		}
	}

	time.Sleep(300 * time.Millisecond)

	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Errorf("expected 1 execution, got %d", count)
	}
}

// TestNoDuplicateWindowOnTripleClick fires three rapid events.
func TestNoDuplicateWindowOnTripleClick(t *testing.T) {
	var mu sync.Mutex
	var execCount int32

	clickCh := make(chan struct{}, 4)
	clickCh <- struct{}{}
	clickCh <- struct{}{}
	clickCh <- struct{}{}

	for i := 0; i < 3; i++ {
		select {
		case <-clickCh:
			if mu.TryLock() {
				go func() {
					defer mu.Unlock()
					atomic.AddInt32(&execCount, 1)
					time.Sleep(100 * time.Millisecond)
				}()
			}
		default:
		}
	}

	time.Sleep(300 * time.Millisecond)

	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Errorf("expected 1 execution, got %d", count)
	}
}

// TestClickAfterCompletion verifies a new click works after the operation finishes.
func TestClickAfterCompletion(t *testing.T) {
	var mu sync.Mutex
	var execCount int32

	clickCh := make(chan struct{}, 4)
	clickCh <- struct{}{}

	// First click.
	<-clickCh
	if mu.TryLock() {
		go func() {
			defer mu.Unlock()
			atomic.AddInt32(&execCount, 1)
			time.Sleep(50 * time.Millisecond)
		}()
	}

	time.Sleep(100 * time.Millisecond)

	// Second click after completion.
	clickCh <- struct{}{}
	<-clickCh
	if mu.TryLock() {
		go func() {
			defer mu.Unlock()
			atomic.AddInt32(&execCount, 1)
			time.Sleep(50 * time.Millisecond)
		}()
	}

	time.Sleep(100 * time.Millisecond)

	if count := atomic.LoadInt32(&execCount); count != 2 {
		t.Errorf("expected 2 executions, got %d", count)
	}
}
