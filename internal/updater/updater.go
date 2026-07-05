// Package updater checks GitHub releases for new versions of z-api-proxy
// and can download + launch the MSI installer for the current architecture.
package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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

// DownloadAndInstall downloads the MSI to a temp file and launches it via
// msiexec. The MSI installer handles the upgrade (WiX MajorUpgrade element).
// The calling process exits so the installer can replace files.
func (r *Release) DownloadAndInstall() error {
	url, err := r.FindMSIURL()
	if err != nil {
		return err
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
	if _, err := io.Copy(out, limitedBody); err != nil {
		os.Remove(msiPath)
		return fmt.Errorf("download write failed: %w", err)
	}
	out.Close()

	log.Printf("updater: launching MSI installer: %s", msiPath)
	if err := exec.Command("msiexec", "/i", msiPath).Start(); err != nil {
		return fmt.Errorf("cannot launch installer: %w", err)
	}

	return nil
}
