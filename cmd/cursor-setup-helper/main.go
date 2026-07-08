// cursor-setup-helper is a standalone CLI tool that configures Cursor
// to use the z-api-proxy Worker. It prompts for the Worker URL and API key,
// then writes them into Cursor's settings.json.
//
// Usage:
//
//	cursor-setup-helper.exe
//
// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"z-api-proxy/internal/config"
)

var modelNames = func() []string {
	mappings := config.DefaultModelMappings()
	names := make([]string, len(mappings))
	for i, m := range mappings {
		names[i] = m.From
	}
	return names
}()

var allModels = "[" + strings.Join(modelNames, ", ") + "]"

func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║   Z-API Proxy — Cursor Setup Helper      ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("This tool configures Cursor to use your z-api-proxy Worker.")
	fmt.Println()

	// Check if Cursor is installed.
	settingsPath := findCursorSettings()
	if settingsPath == "" {
		fmt.Println("ERROR: Cursor installation not found.")
		fmt.Println("       Expected: %APPDATA%\\Cursor\\User\\settings.json")
		fmt.Println("       Install Cursor from https://cursor.com and try again.")
		waitExit(reader)
		return
	}
	fmt.Println("Cursor settings found: " + settingsPath)

	// Check if Cursor is running.
	if cursorRunning() {
		fmt.Println()
		fmt.Println("ERROR: Cursor is currently running!")
		fmt.Println("       Please close Cursor completely before running this tool.")
		fmt.Println("       Cursor will overwrite settings.json on exit if it's running.")
		waitExit(reader)
		return
	}
	fmt.Println("Cursor is not running — OK.")
	fmt.Println()

	// Prompt for Worker URL.
	fmt.Print("Enter your Worker URL (e.g. https://z-api-proxy.sub.workers.dev):\n> ")
	workerURL, _ := reader.ReadString('\n')
	workerURL = strings.TrimSpace(workerURL)
	if workerURL == "" {
		fmt.Println("ERROR: Worker URL is required.")
		waitExit(reader)
		return
	}
	// Ensure URL ends with /v1.
	if !strings.HasSuffix(workerURL, "/v1") {
		workerURL = strings.TrimSuffix(workerURL, "/") + "/v1"
		fmt.Println("  (appended /v1 → " + workerURL + ")")
	}
	fmt.Println()

	// Prompt for API key.
	fmt.Print("Enter the API key for Cursor to send (Gateway Worker Key or z.ai key):\n> ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		fmt.Println("ERROR: API key is required.")
		waitExit(reader)
		return
	}
	fmt.Println()

	// Confirm.
	fmt.Println("Ready to configure Cursor with:")
	fmt.Println("  Base URL: " + workerURL)
	fmt.Println("  API Key:  " + maskKey(apiKey))
	fmt.Println("  Models:   " + fmt.Sprintf("%d z.ai GLM models", len(modelNames)))
	fmt.Println()
	fmt.Print("Proceed? (Y/n): ")
	confirm, _ := reader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))
	if confirm == "n" || confirm == "no" {
		fmt.Println("Cancelled.")
		waitExit(reader)
		return
	}

	// Apply settings.
	err := applySettings(settingsPath, workerURL, apiKey)
	if err != nil {
		fmt.Println("ERROR: " + err.Error())
		waitExit(reader)
		return
	}

	fmt.Println()
	fmt.Println("✓ Cursor configured successfully!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Restart Cursor")
	fmt.Println("  2. Go to Settings → Models")
	fmt.Println("  3. Select a z.ai model (e.g. z.ai/glm-5.2)")
	fmt.Println()
	fmt.Println("  Available models: " + allModels)
	fmt.Println()
	waitExit(reader)
}

// findCursorSettings returns the path to Cursor's settings.json.
func findCursorSettings() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	p := filepath.Join(appData, "Cursor", "User", "settings.json")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// cursorRunning checks if Cursor.exe is running.
func cursorRunning() bool {
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq Cursor.exe", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(out) > 0 && strings.Contains(string(out), "Cursor.exe")
}

// maskKey shows first 8 and last 4 chars of a key.
func maskKey(key string) string {
	if len(key) <= 12 {
		return strings.Repeat("*", len(key))
	}
	return key[:8] + "..." + key[len(key)-4:]
}

// applySettings reads, merges, and writes Cursor's settings.json.
func applySettings(settingsPath, proxyURL, apiKey string) error {
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("cannot read settings: %w", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("cannot parse settings: %w", err)
	}

	settings["cursor.general.openaiApiBaseUrl"] = proxyURL
	settings["cursor.general.enableOpenaiApiBaseUrl"] = true
	settings["cursor.general.openaiApiKey"] = apiKey

	// Merge model names (don't overwrite existing).
	existing, _ := settings["cursor.general.modelNames"].([]interface{})
	seen := make(map[string]bool)
	for _, m := range existing {
		if s, ok := m.(string); ok {
			seen[s] = true
		}
	}
	for _, m := range modelNames {
		if !seen[m] {
			existing = append(existing, m)
		}
	}
	settings["cursor.general.modelNames"] = existing

	// Backup original.
	backupPath := settingsPath + ".bak"
	os.WriteFile(backupPath, raw, 0644)

	out, err := json.MarshalIndent(settings, "", "    ")
	if err != nil {
		return fmt.Errorf("cannot serialize settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return fmt.Errorf("cannot write settings: %w", err)
	}

	return nil
}

// waitExit waits for user to press Enter before exiting.
func waitExit(reader *bufio.Reader) {
	fmt.Println()
	fmt.Print("Press Enter to exit...")
	reader.ReadString('\n')
}
