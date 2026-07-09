package tray

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests call the REAL (*trayApp).guarded method directly (as opposed
// to double_window_test.go, which reimplements the same TryLock/goroutine
// pattern inline). guarded's body never references its receiver, so a
// zero-value &trayApp{} is a valid receiver for exercising it in isolation.

// TestGuarded_SecondCallSkipsWhileFirstRunning proves that calling guarded
// twice, back-to-back, with the same mutex while the first fn is still
// sleeping results in exactly one execution — the second call must return
// immediately (skip + log) rather than block or queue behind the first.
func TestGuarded_SecondCallSkipsWhileFirstRunning(t *testing.T) {
	app := &trayApp{}
	var mu sync.Mutex
	var execCount int32
	started := make(chan struct{})

	app.guarded(&mu, "first", func() {
		close(started)
		time.Sleep(150 * time.Millisecond)
		atomic.AddInt32(&execCount, 1)
	})

	<-started // ensure the first call has actually taken the lock

	// Second call must return immediately (guarded is synchronous about the
	// TryLock decision; only fn itself runs in a goroutine).
	done := make(chan struct{})
	go func() {
		app.guarded(&mu, "second", func() {
			atomic.AddInt32(&execCount, 1)
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("second guarded() call blocked instead of skipping immediately")
	}

	time.Sleep(250 * time.Millisecond) // let the first fn finish

	if count := atomic.LoadInt32(&execCount); count != 1 {
		t.Errorf("expected exactly 1 execution, got %d", count)
	}
}

// TestGuarded_RunsAgainAfterPriorCompletion proves the mutex is correctly
// released via guarded's `defer mu.Unlock()` once fn returns — a later,
// independent guarded() call on the same mutex must still execute.
func TestGuarded_RunsAgainAfterPriorCompletion(t *testing.T) {
	app := &trayApp{}
	var mu sync.Mutex
	var execCount int32

	runAndWait := func() {
		doneRunning := make(chan struct{})
		app.guarded(&mu, "action", func() {
			atomic.AddInt32(&execCount, 1)
			close(doneRunning)
		})
		select {
		case <-doneRunning:
		case <-time.After(time.Second):
			t.Fatal("fn never ran")
		}
		// Give the deferred mu.Unlock() a moment to execute after fn returns.
		time.Sleep(20 * time.Millisecond)
	}

	runAndWait()
	runAndWait()

	if count := atomic.LoadInt32(&execCount); count != 2 {
		t.Errorf("expected 2 executions across two non-overlapping calls, got %d", count)
	}

	// The mutex must be unlocked and available, not stuck locked.
	if !mu.TryLock() {
		t.Error("mutex left locked after guarded()'s fn completed — defer mu.Unlock() did not run")
	} else {
		mu.Unlock()
	}
}

// TestGuarded_DifferentMutexesRunConcurrently proves the guard is per-action
// (per *sync.Mutex), not a single global lock: two "actions" guarded by two
// distinct mutexes must be able to run at the same time without blocking
// each other.
func TestGuarded_DifferentMutexesRunConcurrently(t *testing.T) {
	app := &trayApp{}
	var muA, muB sync.Mutex

	startedA := make(chan struct{})
	startedB := make(chan struct{})
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})

	app.guarded(&muA, "action-a", func() {
		close(startedA)
		<-releaseA
	})
	app.guarded(&muB, "action-b", func() {
		close(startedB)
		<-releaseB
	})

	// Both must start within a short window — if the guard were a single
	// shared lock, the second would never signal "started" until the first
	// released, and this would time out.
	timeout := time.After(time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-startedA:
			startedA = nil
		case <-startedB:
			startedB = nil
		case <-timeout:
			t.Fatal("actions guarded by different mutexes did not run concurrently")
		}
	}

	close(releaseA)
	close(releaseB)
}
