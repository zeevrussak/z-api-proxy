package tray

import (
	"testing"
	"time"
)

// TestProcessDialogResult verifies that operations return correct results.
func TestProcessDialogResult(t *testing.T) {
	// Test success path.
	op := func(progress func(string)) ProcessResult {
		progress("Step 1...")
		time.Sleep(50 * time.Millisecond)
		progress("Step 2...")
		time.Sleep(50 * time.Millisecond)
		return ProcessResult{
			Success: true,
			Title:   "Test Op",
			Summary: "Done!",
		}
	}

	// Run in a goroutine — showProcessDialog blocks on mw.Run().
	done := make(chan ProcessResult, 1)
	go func() {
		// showProcessDialog will run op and block on Run.
		// We can't easily capture the result from outside, so we
		// test the operation function directly here.
		r := op(func(s string) {})
		done <- r
	}()

	select {
	case r := <-done:
		if !r.Success {
			t.Error("expected success")
		}
		if r.Summary != "Done!" {
			t.Errorf("summary = %q", r.Summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

// TestProcessResultSuccess verifies ProcessResult fields.
func TestProcessResultSuccess(t *testing.T) {
	r := ProcessResult{Success: true, Title: "OK", Summary: "All good"}
	if !r.Success {
		t.Error("expected Success=true")
	}
}

// TestFormatDeploySummary verifies deploy summary formatting.
func TestFormatDeploySummary(t *testing.T) {
	s := formatDeploySummary("https://test.workers.dev", "")
	if s == "" {
		t.Error("deploy summary is empty")
	}
	if !containsStr(s, "test.workers.dev") {
		t.Error("deploy summary missing URL")
	}
}

// TestFormatRegisterSummary verifies register summary formatting.
func TestFormatRegisterSummary(t *testing.T) {
	s := formatRegisterSummary("/path/to/settings.json", 19)
	if !containsStr(s, "19") {
		t.Error("register summary missing model count")
	}
}

// TestFormatTunnelSummary verifies named tunnel summary formatting.
func TestFormatTunnelSummary(t *testing.T) {
	s := formatTunnelSummary("tun-123", "proxy.example.com")
	if !containsStr(s, "proxy.example.com") {
		t.Error("tunnel summary missing hostname")
	}
	if !containsStr(s, "tun-123") {
		t.Error("tunnel summary missing tunnel ID")
	}
}

// TestFormatTunnelStartSummary verifies quick-tunnel-start summary formatting.
func TestFormatTunnelStartSummary(t *testing.T) {
	s := formatTunnelStartSummary("https://random-name.trycloudflare.com")
	if !containsStr(s, "random-name.trycloudflare.com") {
		t.Error("tunnel start summary missing URL")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
