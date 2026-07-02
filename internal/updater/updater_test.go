package updater

import "testing"

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
