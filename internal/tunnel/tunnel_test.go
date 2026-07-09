package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withCloudflaredTestServers spins up two test servers standing in for
// the GitHub Releases API and the asset CDN, wires the package override
// vars to point at them, and restores the originals on cleanup.
func withCloudflaredTestServers(t *testing.T, binary []byte, digest string) (releaseSrv, downloadSrv *httptest.Server) {
	t.Helper()

	arch := "amd64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	assetName := "cloudflared-windows-" + arch + ".exe"

	downloadSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	}))

	releaseSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := cloudflaredRelease{
			TagName: "2026.1.0",
			Assets: []cloudflaredAsset{
				{
					Name:               assetName,
					BrowserDownloadURL: downloadSrv.URL + "/" + assetName,
					Digest:             digest,
				},
			},
		}
		json.NewEncoder(w).Encode(rel)
	}))

	origAPI := cloudflaredReleaseAPIOverride
	origBase := cloudflaredDownloadBaseOverride
	cloudflaredReleaseAPIOverride = releaseSrv.URL
	cloudflaredDownloadBaseOverride = downloadSrv.URL + "/unused-"
	t.Cleanup(func() {
		releaseSrv.Close()
		downloadSrv.Close()
		cloudflaredReleaseAPIOverride = origAPI
		cloudflaredDownloadBaseOverride = origBase
	})
	return releaseSrv, downloadSrv
}

func TestEnsureDownloaded_ChecksumMatch(t *testing.T) {
	binary := []byte("fake cloudflared binary contents")
	sum := sha256.Sum256(binary)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	withCloudflaredTestServers(t, binary, digest)

	m := &Manager{cacheDir: t.TempDir()}
	if err := m.ensureDownloaded(); err != nil {
		t.Fatalf("ensureDownloaded: %v", err)
	}

	got, err := os.ReadFile(m.cloudflaredPath())
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(binary) {
		t.Error("downloaded file contents do not match expected binary")
	}
	if _, err := os.Stat(m.cloudflaredPath() + ".download"); err == nil {
		t.Error("leftover .download temp file was not cleaned up")
	}
}

func TestEnsureDownloaded_ChecksumMismatchRejected(t *testing.T) {
	binary := []byte("fake cloudflared binary contents")
	wrongDigest := "sha256:" + strings.Repeat("0", 64)

	withCloudflaredTestServers(t, binary, wrongDigest)

	m := &Manager{cacheDir: t.TempDir()}
	err := m.ensureDownloaded()
	if err == nil {
		t.Fatal("expected checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want checksum mismatch error", err)
	}
	if _, statErr := os.Stat(m.cloudflaredPath()); statErr == nil {
		t.Error("cloudflared.exe should not exist after a checksum mismatch")
	}
	if _, statErr := os.Stat(m.cloudflaredPath() + ".download"); statErr == nil {
		t.Error("temp .download file should be removed after a checksum mismatch")
	}
}

func TestEnsureDownloaded_MissingDigestFallsBackUnverified(t *testing.T) {
	binary := []byte("fake cloudflared binary contents")
	// No digest published for this asset (e.g. an older or unusual
	// release) — should still succeed, just without verification.
	withCloudflaredTestServers(t, binary, "")

	m := &Manager{cacheDir: t.TempDir()}
	if err := m.ensureDownloaded(); err != nil {
		t.Fatalf("ensureDownloaded should fall back to unverified download: %v", err)
	}
	got, err := os.ReadFile(m.cloudflaredPath())
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(binary) {
		t.Error("downloaded file contents do not match expected binary")
	}
}

func TestEnsureDownloaded_ReleaseAPIUnreachableFallsBackUnverified(t *testing.T) {
	binary := []byte("fake cloudflared binary contents")
	downloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	}))
	defer downloadSrv.Close()

	origAPI := cloudflaredReleaseAPIOverride
	origBase := cloudflaredDownloadBaseOverride
	// Point the release API at an address nothing is listening on.
	cloudflaredReleaseAPIOverride = "http://127.0.0.1:1"
	cloudflaredDownloadBaseOverride = downloadSrv.URL + "/cloudflared-windows-"
	defer func() {
		cloudflaredReleaseAPIOverride = origAPI
		cloudflaredDownloadBaseOverride = origBase
	}()

	m := &Manager{cacheDir: t.TempDir()}
	if err := m.ensureDownloaded(); err != nil {
		t.Fatalf("ensureDownloaded should fall back to unverified download when the release API is unreachable: %v", err)
	}
	if !m.IsDownloaded() {
		t.Error("expected cloudflared.exe to be downloaded despite release API being unreachable")
	}
}

func TestEnsureDownloaded_SkipsIfAlreadyCached(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{cacheDir: dir}
	existing := filepath.Join(dir, cloudflaredExeName)
	if err := os.WriteFile(existing, []byte("already here"), 0644); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	// No servers wired up — if ensureDownloaded tried to hit the
	// network it would fail, proving the cache short-circuit works.
	if err := m.ensureDownloaded(); err != nil {
		t.Fatalf("ensureDownloaded should skip network entirely when cached: %v", err)
	}
}
