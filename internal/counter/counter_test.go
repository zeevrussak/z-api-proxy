package counter

import (
	"sync"
	"testing"
)

func TestCounter_ZeroValue(t *testing.T) {
	c := New()
	if got := c.Handled(); got != 0 {
		t.Errorf("Handled() = %d, want 0 for a new Counter", got)
	}
	if got := c.Rejected(); got != 0 {
		t.Errorf("Rejected() = %d, want 0 for a new Counter", got)
	}
}

func TestCounter_IncrementAndRead(t *testing.T) {
	c := New()
	c.IncHandled()
	c.IncHandled()
	c.IncHandled()
	c.IncRejected()

	if got := c.Handled(); got != 3 {
		t.Errorf("Handled() = %d, want 3", got)
	}
	if got := c.Rejected(); got != 1 {
		t.Errorf("Rejected() = %d, want 1", got)
	}
}

func TestCounter_HandledAndRejectedAreIndependent(t *testing.T) {
	c := New()
	c.IncRejected()
	if got := c.Handled(); got != 0 {
		t.Errorf("Handled() = %d, want 0 after only IncRejected calls", got)
	}
}

// TestCounter_ConcurrentIncrement drives IncHandled/IncRejected from many
// goroutines simultaneously. Counter is used from the proxy's ServeHTTP
// (called concurrently per-request) and read from the tray's tooltip
// goroutine at the same time — this test exists to prove the atomic
// counters never lose an increment under real concurrent load. Run with
// `go test -race` to also catch any accidental non-atomic access.
func TestCounter_ConcurrentIncrement(t *testing.T) {
	c := New()
	const goroutines = 50
	const perGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				c.IncHandled()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				c.IncRejected()
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * perGoroutine)
	if got := c.Handled(); got != want {
		t.Errorf("Handled() = %d, want %d (lost increments under concurrency)", got, want)
	}
	if got := c.Rejected(); got != want {
		t.Errorf("Rejected() = %d, want %d (lost increments under concurrency)", got, want)
	}
}

// TestCounter_ConcurrentReadDuringWrite exercises the pattern tray.go
// actually uses: one goroutine incrementing while another concurrently
// reads Handled()/Rejected() in a loop (mirrors updateTooltip's 1s poll
// racing against proxy.ServeHTTP's increments).
func TestCounter_ConcurrentReadDuringWrite(t *testing.T) {
	c := New()
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				c.IncHandled()
				c.IncRejected()
			}
		}
	}()

	// Read concurrently on the main test goroutine while the writer
	// above is still incrementing.
	for i := 0; i < 1000; i++ {
		_ = c.Handled()
		_ = c.Rejected()
	}

	close(done)
	wg.Wait()
}
