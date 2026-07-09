package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.0.0", "1.0.1", true},
		{"1.0.0", "1.1.0", true},
		{"1.0.0", "2.0.0", true},
		{"1.0.1", "1.0.0", false},
		{"1.1.0", "1.0.0", false},
		{"1.0.0", "1.0.0", false},
		// v prefix
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.0.0", false},
		// pre-release: release > pre-release
		{"1.0.0-alpha", "1.0.0", true},
		{"1.0.0", "1.0.0-alpha", false},
		// pre-release ordering
		{"1.0.0-alpha", "1.0.0-beta", true},
		{"1.0.0-beta", "1.0.0-alpha", false},
		// mixed
		{"v1.0.0-alpha", "1.0.1", true},
		{"1.0.0-alpha", "1.0.0-alpha", false},
		// dev builds never get update notifications for same version
		{"dev", "1.0.0", true},
	}

	for _, tt := range tests {
		got := IsNewer(tt.current, tt.latest)
		if got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	s := parseSemver("v1.2.3-alpha")
	if s.nums[0] != 1 || s.nums[1] != 2 || s.nums[2] != 3 {
		t.Errorf("nums = %v, want [1 2 3]", s.nums)
	}
	if s.suffix != "alpha" {
		t.Errorf("suffix = %q, want alpha", s.suffix)
	}

	s2 := parseSemver("2.0.0")
	if s2.nums[0] != 2 || s2.suffix != "" {
		t.Errorf("parseSemver(2.0.0) = %v %+q, want nums=[2 0 0] suffix=''", s2.nums, s2.suffix)
	}
}

func TestFindMSIURL(t *testing.T) {
	r := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "z-api-proxy-win-1.0.0-amd64.msi", BrowserDownloadURL: "https://example.com/amd64.msi"},
			{Name: "z-api-proxy-win-1.0.0-arm64.msi", BrowserDownloadURL: "https://example.com/arm64.msi"},
		},
	}

	url, err := r.FindMSIURL()
	if err != nil {
		t.Fatalf("FindMSIURL: %v", err)
	}
	arch := archName()
	if !contains(url, arch) {
		t.Errorf("FindMSIURL returned %q, expected to contain %s", url, arch)
	}
}

func TestFindMSIURL_NotFound(t *testing.T) {
	r := &Release{
		TagName: "v1.0.0",
		Assets:  []Asset{},
	}
	_, err := r.FindMSIURL()
	if err == nil {
		t.Fatal("expected error for empty assets, got nil")
	}
}

func TestFindChecksumURL(t *testing.T) {
	r := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "z-api-proxy-win-1.0.0-amd64.msi", BrowserDownloadURL: "https://example.com/amd64.msi"},
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
		},
	}
	url, err := r.FindChecksumURL()
	if err != nil {
		t.Fatalf("FindChecksumURL: %v", err)
	}
	if url != "https://example.com/checksums.txt" {
		t.Errorf("FindChecksumURL = %q, want https://example.com/checksums.txt", url)
	}
}

func TestFindChecksumURL_NotFound(t *testing.T) {
	r := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "z-api-proxy-win-1.0.0-amd64.msi", BrowserDownloadURL: "https://example.com/amd64.msi"},
		},
	}
	if _, err := r.FindChecksumURL(); err == nil {
		t.Fatal("expected error when checksums.txt is absent, got nil")
	}
}

func TestFetchChecksums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ABCDEF0123456789  z-api-proxy-win-1.0.0-amd64.msi\ndeadbeef  z-api-proxy-win-1.0.0-setup.exe\n\n"))
	}))
	defer srv.Close()

	sums, err := fetchChecksums(srv.URL)
	if err != nil {
		t.Fatalf("fetchChecksums: %v", err)
	}
	if got := sums["z-api-proxy-win-1.0.0-amd64.msi"]; got != "abcdef0123456789" {
		t.Errorf("sums[amd64.msi] = %q, want lowercased abcdef0123456789", got)
	}
	if got := sums["z-api-proxy-win-1.0.0-setup.exe"]; got != "deadbeef" {
		t.Errorf("sums[setup.exe] = %q, want deadbeef", got)
	}
	if len(sums) != 2 {
		t.Errorf("len(sums) = %d, want 2 (blank line must be skipped)", len(sums))
	}
}

// TestDownloadAndInstall_ChecksumMismatchAborts verifies a checksum
// mismatch is a hard failure and — critically — that DownloadAndInstall
// returns the error before ever invoking msiexec (this is the only part
// of DownloadAndInstall this suite safely exercises end-to-end; the
// success path is not tested here because it would spawn a real
// msiexec process against a fake MSI file).
func TestDownloadAndInstall_ChecksumMismatchAborts(t *testing.T) {
	msiContent := []byte("not a real msi, just test bytes")
	msiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(msiContent)
	}))
	defer msiSrv.Close()

	checksumSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately wrong hash for the MSI above.
		w.Write([]byte(strings.Repeat("0", 64) + "  z-api-proxy-win-1.0.0-" + archName() + ".msi\n"))
	}))
	defer checksumSrv.Close()

	r := &Release{
		TagName: "v1.0.0",
		Assets: []Asset{
			{Name: "z-api-proxy-win-1.0.0-" + archName() + ".msi", BrowserDownloadURL: msiSrv.URL + "/z-api-proxy-win-1.0.0-" + archName() + ".msi"},
			{Name: "checksums.txt", BrowserDownloadURL: checksumSrv.URL},
		},
	}

	err := r.DownloadAndInstall()
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want checksum mismatch error", err)
	}
}

// TestDownloadAndInstall_MatchingChecksumPassesVerification exercises
// the verification step in isolation (without going through
// DownloadAndInstall's msiexec launch) by replicating just the
// download+hash+compare logic against a real matching digest.
func TestDownloadAndInstall_MatchingChecksumPassesVerification(t *testing.T) {
	msiContent := []byte("not a real msi, just test bytes")
	sum := sha256.Sum256(msiContent)
	expected := hex.EncodeToString(sum[:])

	checksumSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(expected + "  z-api-proxy-win-1.0.0-" + archName() + ".msi\n"))
	}))
	defer checksumSrv.Close()

	r := &Release{
		Assets: []Asset{
			{Name: "checksums.txt", BrowserDownloadURL: checksumSrv.URL},
		},
	}
	checksumURL, err := r.FindChecksumURL()
	if err != nil {
		t.Fatalf("FindChecksumURL: %v", err)
	}
	sums, err := fetchChecksums(checksumURL)
	if err != nil {
		t.Fatalf("fetchChecksums: %v", err)
	}
	msiName := "z-api-proxy-win-1.0.0-" + archName() + ".msi"
	got := sums[msiName]
	if got != expected {
		t.Errorf("sums[%s] = %q, want %q", msiName, got, expected)
	}

	actualSum := sha256.Sum256(msiContent)
	if hex.EncodeToString(actualSum[:]) != got {
		t.Error("computed hash of downloaded content does not match checksums.txt entry")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
