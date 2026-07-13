package main

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestUISettingsWindow builds the exe with -H windowsgui and runs it
// with --test-ui. The exe opens the settings window, waits 3 seconds,
// then exits 0 on success or non-zero on failure.
//
// This is a real integration test — it launches the actual GUI binary.
// It must run on Windows with a desktop session available.
func TestUISettingsWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UI integration test in short mode")
	}

	// Build the test binary.
	exePath := filepath.Join(t.TempDir(), "z-api-proxy-test.exe")
	build := exec.Command("go", "build", "-ldflags", "-H windowsgui", "-o", exePath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Run with --test-ui flag. It opens the settings window, waits 3s, exits.
	cmd := exec.Command(exePath, "--test-ui")

	// Give it 30 seconds to complete (3s internal wait + startup).
	timer := time.AfterFunc(30*time.Second, func() {
		cmd.Process.Kill()
	})
	defer timer.Stop()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("settings window test failed: %v", err)
	}

	// Exit code 0 = window opened and closed successfully.
	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("settings window test exited with code %d", cmd.ProcessState.ExitCode())
	}
}

// TestUIProgressWindowSingleInstance builds the exe with -H windowsgui and
// runs it with --test-progress. The exe drives a real progress window
// through several status updates, counting live windows with its title via
// EnumWindows, and exits 0 only if exactly one such window ever existed at
// once (and it was observed at least once).
//
// This is the regression test for the "Deploy Cloudflare Worker opens
// multiple progress windows" bug: before the fix, process_dialog.go called
// declarative.MainWindow.Create() and then declarative.MainWindow.Run() on
// the same value — Run() unconditionally calls Create() again, opening a
// second native window every single time, not just on double-clicks. This
// test would have failed against that code (it would have observed 2
// windows with the same title simultaneously).
//
// Like TestUISettingsWindow, this is a real integration test — it launches
// the actual GUI binary and requires a real Windows desktop session.
func TestUIProgressWindowSingleInstance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping UI integration test in short mode")
	}

	exePath := filepath.Join(t.TempDir(), "z-api-proxy-test-progress.exe")
	build := exec.Command("go", "build", "-ldflags", "-H windowsgui", "-o", exePath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(exePath, "--test-progress")

	timer := time.AfterFunc(30*time.Second, func() {
		cmd.Process.Kill()
	})
	defer timer.Stop()

	err := cmd.Run()
	if err != nil {
		t.Fatalf("progress window test failed: %v", err)
	}

	if cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("progress window test exited with code %d — a duplicate progress window was likely observed", cmd.ProcessState.ExitCode())
	}
}
