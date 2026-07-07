package tray

import (
	"testing"
	"time"
)

// TestProcessDialogLifecycle verifies the operation completes and returns
// a result via the done channel. The window may fail to create in test
// mode (no desktop session) — we verify the operation runs regardless.
func TestProcessDialogLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UI test in short mode")
	}

	op := func(progress func(string)) ProcessResult {
		progress("Step 1: checking...")
		time.Sleep(100 * time.Millisecond)
		progress("Step 2: working...")
		time.Sleep(100 * time.Millisecond)
		return ProcessResult{
			Success: true,
			Title:   "Test Operation",
			Summary: "Completed successfully!",
		}
	}

	_, done := StartProcessDialog("Test Dialog", "Working", op)

	// Wait for completion (with timeout).
	select {
	case r := <-done:
		if !r.Success {
			t.Error("operation failed")
		}
		if r.Summary != "Completed successfully!" {
			t.Errorf("summary = %q, want 'Completed successfully!'", r.Summary)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("operation timed out")
	}
}

// TestProcessDialogFailure verifies the dialog handles operation failures.
func TestProcessDialogFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UI test in short mode")
	}

	op := func(progress func(string)) ProcessResult {
		progress("Failing...")
		time.Sleep(50 * time.Millisecond)
		return ProcessResult{
			Success: false,
			Title:   "Test Operation",
			Summary: "Something went wrong!",
		}
	}

	_, done := StartProcessDialog("Test Dialog Fail", "Failing", op)

	select {
	case r := <-done:
		if r.Success {
			t.Error("expected failure, got success")
		}
		if r.Summary != "Something went wrong!" {
			t.Errorf("summary = %q", r.Summary)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("operation timed out")
	}
}

// TestProcessDialogProgress verifies progress callbacks work.
func TestProcessDialogProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UI test in short mode")
	}

	progressTexts := []string{}
	op := func(progress func(string)) ProcessResult {
		for _, s := range []string{"A", "B", "C"} {
			progress(s)
			progressTexts = append(progressTexts, s)
			time.Sleep(50 * time.Millisecond)
		}
		return ProcessResult{Success: true, Title: "Done", Summary: "All steps complete"}
	}

	_, done := StartProcessDialog("Progress Test", "Running", op)
	<-done

	if len(progressTexts) != 3 {
		t.Errorf("expected 3 progress calls, got %d", len(progressTexts))
	}
}

// TestFormatDeploySummary verifies deploy summary formatting.
func TestFormatDeploySummary(t *testing.T) {
	s := formatDeploySummary("https://z-api-proxy.test.workers.dev", "")
	if s == "" {
		t.Error("deploy summary is empty")
	}
	if !contains(s, "test.workers.dev") {
		t.Error("deploy summary missing URL")
	}
}

// TestFormatRegisterSummary verifies register summary formatting.
func TestFormatRegisterSummary(t *testing.T) {
	s := formatRegisterSummary("/path/to/settings.json", 19)
	if !contains(s, "19") {
		t.Error("register summary missing model count")
	}
}

// TestFormatTunnelSummary verifies tunnel summary formatting.
func TestFormatTunnelSummary(t *testing.T) {
	s := formatTunnelSummary("proxy.example.com")
	if !contains(s, "proxy.example.com") {
		t.Error("tunnel summary missing hostname")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
