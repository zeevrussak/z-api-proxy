// clientip.go keeps the Cloudflare API token's Client IP Address
// Filtering condition (condition.request_ip) in sync with this
// machine's current external IP.
//
// Why this exists: cfg.Cloudflare.APIToken is the same token used
// throughout this package to deploy the Worker. If that token has an
// IP restriction on Cloudflare's side and the machine's external IP
// changes (e.g. dynamic residential IP), the token silently stops
// working until someone manually updates it in the dashboard. This
// file automates that update, gated behind
// cfg.Cloudflare.AutoUpdateClientIP (opt-in, default false — see
// config.CloudflareConfig).
//
// Token endpoints (/user/tokens/...) are user-scoped, not
// account-scoped — cfg.Cloudflare.AccountID is not used here.
package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"z-api-proxy/internal/config"
)

// externalIPTraceURL is Cloudflare's own IP-echo endpoint. Reusing a
// Cloudflare-owned endpoint (instead of a third-party "what's my IP"
// service) avoids introducing a new external dependency/trust boundary
// in an already Cloudflare-centric app.
const externalIPTraceURL = "https://www.cloudflare.com/cdn-cgi/trace"

// fetchExternalIPOverride lets tests inject a fake IP lookup instead of
// hitting the real internet, mirroring apiBaseOverride's role for the
// Cloudflare API client below.
var fetchExternalIPOverride = fetchExternalIPFromCloudflare

// fetchExternalIPFromCloudflare queries Cloudflare's trace endpoint and
// parses the "ip=" line out of its plaintext (not JSON) response, e.g.:
//
//	fl=123abc
//	ip=203.0.113.5
//	ts=1234567890.123
//	...
func fetchExternalIPFromCloudflare() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(externalIPTraceURL)
	if err != nil {
		return "", fmt.Errorf("trace request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("trace read failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("trace endpoint returned HTTP %d", resp.StatusCode)
	}
	return parseTraceIP(string(body))
}

// parseTraceIP extracts the value of the "ip=" line from a Cloudflare
// /cdn-cgi/trace plaintext response body. Split out from
// fetchExternalIPFromCloudflare so the parsing logic can be unit
// tested without any network access.
func parseTraceIP(body string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if ip, ok := strings.CutPrefix(line, "ip="); ok {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				return "", fmt.Errorf("trace response had empty ip= value")
			}
			return ip, nil
		}
	}
	return "", fmt.Errorf("no ip= line found in trace response")
}

// externalIPPrefPath returns where the last-known external IP is
// cached — alongside the other small preference files (worker.pref,
// tunnel.pref, worker-testkey.pref).
func externalIPPrefPath() string {
	return filepath.Join(config.AppConfigDir(), "external-ip.pref")
}

func loadLastKnownIP() string {
	data, err := os.ReadFile(externalIPPrefPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveLastKnownIP(ip string) error {
	return os.WriteFile(externalIPPrefPath(), []byte(ip), 0600)
}

// tokenRequestIP mirrors Cloudflare's condition.request_ip object.
type tokenRequestIP struct {
	In    []string `json:"in,omitempty"`
	NotIn []string `json:"not_in,omitempty"`
}

// tokenCondition mirrors Cloudflare's token condition object.
type tokenCondition struct {
	RequestIP *tokenRequestIP `json:"request_ip,omitempty"`
}

// tokenDefinition is the subset of the Cloudflare API token schema
// needed to round-trip a GET /user/tokens/{id} response into a PUT
// /user/tokens/{id} request (PUT replaces the whole token — Cloudflare
// has no partial-patch endpoint for tokens). Policies is kept as raw
// JSON rather than fully modeled: it's the structurally complex part
// of a token (nested permission groups, resource maps), and since this
// feature only ever needs to touch condition.request_ip, round-tripping
// it byte-for-byte avoids any risk of the PUT silently dropping or
// mangling permissions this code doesn't understand.
type tokenDefinition struct {
	Name      string          `json:"name"`
	Policies  json.RawMessage `json:"policies"`
	Condition *tokenCondition `json:"condition,omitempty"`
	ExpiresOn string          `json:"expires_on,omitempty"`
	NotBefore string          `json:"not_before,omitempty"`
	Status    string          `json:"status,omitempty"`
}

// verifyTokenID calls GET /user/tokens/verify so the token
// self-identifies its own ID. This is the supported way to discover a
// token's ID: there is no "list all my tokens" call guaranteed to
// return exactly the one currently in use, and guessing/listing would
// be far less reliable than asking the token to verify itself.
func verifyTokenID(client *http.Client, apiToken string) (string, error) {
	req, err := http.NewRequest("GET", apiBaseOverride+"/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("verify request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var cfErr cfResponse
		json.Unmarshal(body, &cfErr)
		return "", fmt.Errorf("verify returned HTTP %d: %s", resp.StatusCode, cfErr.ErrorString())
	}

	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("cannot parse verify response: %w", err)
	}
	if result.Result.ID == "" {
		return "", fmt.Errorf("verify response had no token id")
	}
	return result.Result.ID, nil
}

// getTokenDefinition fetches the full token definition via GET
// /user/tokens/{id}. Needed because PUT replaces the entire token — a
// partial patch would clobber unrelated fields (name, policies, expiry)
// with their zero values.
func getTokenDefinition(client *http.Client, apiToken, tokenID string) (*tokenDefinition, error) {
	req, err := http.NewRequest("GET", apiBaseOverride+"/user/tokens/"+tokenID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var cfErr cfResponse
		json.Unmarshal(body, &cfErr)
		return nil, fmt.Errorf("get token returned HTTP %d: %s", resp.StatusCode, cfErr.ErrorString())
	}

	var wrapper struct {
		Result tokenDefinition `json:"result"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("cannot parse token response: %w", err)
	}
	return &wrapper.Result, nil
}

// putTokenDefinition pushes the (mutated) full token definition back
// via PUT /user/tokens/{id}. def must be the complete definition with
// only the intended field(s) changed — see getTokenDefinition.
func putTokenDefinition(client *http.Client, apiToken, tokenID string, def *tokenDefinition) error {
	bodyJSON, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("cannot marshal token definition: %w", err)
	}
	req, err := http.NewRequest("PUT", apiBaseOverride+"/user/tokens/"+tokenID, bytes.NewReader(bodyJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("put token request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		var cfErr cfResponse
		json.Unmarshal(respBody, &cfErr)
		return fmt.Errorf("put token returned HTTP %d: %s", resp.StatusCode, cfErr.ErrorString())
	}
	return nil
}

// updateTokenIPCondition performs verify -> GET -> PUT to point the
// token's condition.request_ip.in at exactly newIP/32, preserving any
// existing not_in entries rather than clobbering them.
func updateTokenIPCondition(apiToken, newIP string) error {
	client := &http.Client{Timeout: 15 * time.Second}

	tokenID, err := verifyTokenID(client, apiToken)
	if err != nil {
		return fmt.Errorf("cannot self-identify token: %w", err)
	}

	def, err := getTokenDefinition(client, apiToken, tokenID)
	if err != nil {
		return fmt.Errorf("cannot fetch current token definition: %w", err)
	}

	var notIn []string
	if def.Condition != nil && def.Condition.RequestIP != nil {
		notIn = def.Condition.RequestIP.NotIn
	}
	def.Condition = &tokenCondition{
		RequestIP: &tokenRequestIP{
			In:    []string{newIP + "/32"},
			NotIn: notIn,
		},
	}

	if err := putTokenDefinition(client, apiToken, tokenID, def); err != nil {
		return fmt.Errorf("cannot update token IP condition: %w", err)
	}
	return nil
}

// UpdateClientIPIfChanged is the entry point called by the tray's
// background poller. It never panics and never leaves the token in a
// half-updated state: on any failure it returns an error for the
// caller to log and leaves both the token and the persisted IP
// untouched, so the next poll simply retries.
//
// Behavior:
//   - Feature disabled (cfg.Cloudflare.AutoUpdateClientIP == false):
//     no-op, returns nil without any network call.
//   - No APIToken configured: no-op, returns nil (logs once so it's
//     visible in proxy.log, but this is not treated as an error since
//     the feature simply has nothing to act on yet).
//   - First run (no persisted IP yet) or IP changed since last known:
//     runs the verify -> GET -> PUT sequence to point the token's
//     condition.request_ip.in at the current IP, then persists it only
//     on success. First run intentionally attempts a real update
//     rather than just baselining silently — the whole point of this
//     feature is to fix a token whose IP restriction has already
//     drifted, and that can only happen by enabling this option and
//     letting the very next poll push a correction. If the token
//     already matches (freshly issued or already correctly scoped),
//     this call is a harmless no-op PUT of the same value.
//   - IP unchanged since last known: no-op, no API call at all.
//
// Chicken-and-egg edge case: if the token's IP restriction already
// excludes this machine's current external IP *before* this feature
// gets a chance to run, every call the token makes — including the
// self-identifying verify call this function starts with — is
// rejected by Cloudflare at the edge before reaching any of this
// logic. This feature cannot bootstrap a token out of that state; the
// user must first manually widen/fix the restriction in the Cloudflare
// dashboard to include the current IP. From that point on, this
// feature keeps it in sync as the IP continues to drift.
func UpdateClientIPIfChanged(cfg *config.Config) error {
	if !cfg.Cloudflare.AutoUpdateClientIP {
		return nil
	}
	if cfg.Cloudflare.APIToken == "" {
		log.Printf("clientip: auto_update_client_ip is enabled but cloudflare.api_token is not set — skipping")
		return nil
	}

	currentIP, err := fetchExternalIPOverride()
	if err != nil {
		return fmt.Errorf("cannot determine external IP: %w", err)
	}

	lastIP := loadLastKnownIP()
	if lastIP != "" && currentIP == lastIP {
		return nil
	}

	if lastIP == "" {
		log.Printf("clientip: no baseline IP on record — syncing Cloudflare token IP restriction to current external IP %s", currentIP)
	} else {
		log.Printf("clientip: external IP changed %s -> %s, updating Cloudflare token IP restriction", lastIP, currentIP)
	}

	if err := updateTokenIPCondition(cfg.Cloudflare.APIToken, currentIP); err != nil {
		return fmt.Errorf("failed to update Cloudflare token IP restriction (token left unchanged, will retry next poll): %w", err)
	}
	if err := saveLastKnownIP(currentIP); err != nil {
		return fmt.Errorf("token updated successfully but failed to persist new IP %s (will re-attempt the update next poll): %w", currentIP, err)
	}

	log.Printf("clientip: Cloudflare token IP restriction updated to %s/32", currentIP)
	return nil
}
