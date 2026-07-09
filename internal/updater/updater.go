// Package updater checks GitHub releases for new versions of z-api-proxy
// and can download + launch the MSI installer for the current architecture.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	repoOwner = "zeevrussak"
	repoName  = "z-api-proxy"
	apiURL    = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
)

// Release represents the relevant fields from the GitHub releases API.
type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

// Asset is a downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// FetchLatest queries the GitHub API for the latest release.
func FetchLatest() (*Release, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// IsNewer compares the current version against the latest tag and reports
// whether the latest is newer. Both versions may have a leading 'v' prefix
// and pre-release suffixes (e.g. "v1.2.0-alpha").
func IsNewer(current, latest string) bool {
	cv := parseSemver(current)
	lv := parseSemver(latest)
	for i := 0; i < 3; i++ {
		if lv.nums[i] != cv.nums[i] {
			return lv.nums[i] > cv.nums[i]
		}
	}
	// Same major.minor.patch: a release version (no suffix) is newer than
	// a pre-release version (has suffix).
	if cv.suffix == "" && lv.suffix != "" {
		return false
	}
	if cv.suffix != "" && lv.suffix == "" {
		return true
	}
	return lv.suffix > cv.suffix
}

type semver struct {
	nums   [3]int
	suffix string
}

func parseSemver(v string) semver {
	v = strings.TrimPrefix(v, "v")
	s := semver{}
	parts := strings.SplitN(v, "-", 2)
	if len(parts) == 2 {
		s.suffix = parts[1]
	}
	numParts := strings.Split(parts[0], ".")
	for i := 0; i < 3 && i < len(numParts); i++ {
		n, _ := strconv.Atoi(numParts[i])
		s.nums[i] = n
	}
	return s
}

// archName returns the GOARCH string used in MSI asset filenames.
func archName() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// FindMSIURL returns the download URL for the MSI matching the current
// architecture from the release assets.
func (r *Release) FindMSIURL() (string, error) {
	arch := archName()
	for _, a := range r.Assets {
		if strings.HasSuffix(a.Name, "-"+arch+".msi") {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no MSI found for %s in release %s", arch, r.TagName)
}

// FindChecksumURL returns the download URL for checksums.txt in the
// release, if published (build.bat generates it starting with the
// release that ships this check). Older releases predate it — callers
// must treat a "not found" error as "skip verification", not fatal.
func (r *Release) FindChecksumURL() (string, error) {
	for _, a := range r.Assets {
		if a.Name == "checksums.txt" {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("no checksums.txt in release %s", r.TagName)
}

// fetchChecksums downloads and parses a sha256sum-style checksums.txt
// (lines of "<hex sha256>  <filename>", as produced by build.bat / the
// Windows certutil -hashfile tool) into a filename → hash map.
func fetchChecksums(url string) (map[string]string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("checksums download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checksums download returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB is far more than enough
	if err != nil {
		return nil, fmt.Errorf("checksums read failed: %w", err)
	}

	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sums[fields[1]] = strings.ToLower(fields[0])
	}
	return sums, nil
}

// DownloadAndInstall downloads the MSI to a temp file and launches it via
// msiexec. The MSI installer handles the upgrade (WiX MajorUpgrade element).
// The calling process exits so the installer can replace files.
//
// Integrity: if the release publishes a checksums.txt (see build.bat),
// the downloaded MSI's SHA-256 is verified against it before msiexec is
// invoked. This defends against corruption or tampering in transit —
// it does NOT defend against a compromised release process or repo,
// since checksums.txt is fetched from the same GitHub release as the
// MSI. That would require code signing, which is out of scope here.
// A missing checksums.txt (older releases) is a warning, not a hard
// failure; a checksum MISMATCH against a present manifest IS a hard
// failure and aborts the install.
func (r *Release) DownloadAndInstall() error {
	url, err := r.FindMSIURL()
	if err != nil {
		return err
	}
	msiName := path.Base(url)

	var expectedSHA256 string
	if checksumURL, cerr := r.FindChecksumURL(); cerr != nil {
		log.Printf("updater: no checksums.txt published for release %s (continuing without verification): %v", r.TagName, cerr)
	} else if sums, serr := fetchChecksums(checksumURL); serr != nil {
		log.Printf("updater: warning — could not fetch checksums.txt: %v (continuing without verification)", serr)
	} else if h, ok := sums[msiName]; ok {
		expectedSHA256 = h
	} else {
		log.Printf("updater: warning — checksums.txt has no entry for %s (continuing without verification)", msiName)
	}

	log.Printf("updater: downloading %s", url)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	// Cap download at 200MB.
	const maxDownloadSize = 200 << 20
	limitedBody := io.LimitReader(resp.Body, maxDownloadSize)

	out, err := os.CreateTemp(os.TempDir(), "z-api-proxy-update-*.msi")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	msiPath := out.Name()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hasher), limitedBody); err != nil {
		out.Close()
		os.Remove(msiPath)
		return fmt.Errorf("download write failed: %w", err)
	}
	out.Close()

	if expectedSHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if got != expectedSHA256 {
			os.Remove(msiPath)
			return fmt.Errorf("checksum mismatch for %s: expected %s, got %s — download may be corrupted or tampered with, aborting install", msiName, expectedSHA256, got)
		}
		log.Printf("updater: checksum verified for %s", msiName)
	}

	log.Printf("updater: launching MSI installer: %s", msiPath)
	if err := exec.Command("msiexec", "/i", msiPath).Start(); err != nil {
		return fmt.Errorf("cannot launch installer: %w", err)
	}

	return nil
}
