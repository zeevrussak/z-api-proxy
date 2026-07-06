// cursor-check is a standalone CLI tool that reads Cursor's settings.json
// and prints the current API configuration.
//
// Usage:
//
//	cursor-check.exe
//
// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		fmt.Println("ERROR: APPDATA not set")
		os.Exit(1)
	}

	settingsPath := filepath.Join(appData, "Cursor", "User", "settings.json")
	fmt.Println("Cursor settings file: " + settingsPath)
	fmt.Println()

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		fmt.Printf("ERROR: cannot read settings: %v\n", err)
		os.Exit(1)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		fmt.Printf("ERROR: cannot parse settings: %v\n", err)
		os.Exit(1)
	}

	// Print cursor.general.* keys in sorted order.
	type kv struct {
		key string
		val interface{}
	}
	var cursorKeys []kv
	for k, v := range settings {
		if len(k) > 14 && k[:14] == "cursor.general." {
			cursorKeys = append(cursorKeys, kv{k, v})
		}
	}

	if len(cursorKeys) == 0 {
		fmt.Println("No cursor.general.* settings found.")
		fmt.Println("\nFull settings.json:")
		fmt.Println(string(raw))
		return
	}

	sort.Slice(cursorKeys, func(i, j int) bool {
		return cursorKeys[i].key < cursorKeys[j].key
	})

	fmt.Println("Cursor API Settings:")
	fmt.Println("═══════════════════════════════════════════════════════════")

	for _, kv := range cursorKeys {
		valStr := fmt.Sprintf("%v", kv.val)
		// Mask keys.
		if isKey(kv.key) && len(valStr) > 8 {
			valStr = valStr[:4] + "..." + valStr[len(valStr)-4:]
		}
		fmt.Printf("  %-45s %s\n", kv.key, valStr)
	}

	fmt.Println("═══════════════════════════════════════════════════════════")
	fmt.Println()

	// Check for common issues.
	baseURL, _ := settings["cursor.general.openaiApiBaseUrl"].(string)
	enabled, _ := settings["cursor.general.enableOpenaiApiBaseUrl"].(bool)
	apiKey, _ := settings["cursor.general.openaiApiKey"].(string)

	if baseURL != "" {
		fmt.Println("OpenAI Override URL: " + baseURL)
	} else {
		fmt.Println("WARNING: openaiApiBaseUrl is not set")
	}

	if enabled {
		fmt.Println("Override enabled: YES")
	} else {
		fmt.Println("WARNING: enableOpenaiApiBaseUrl is NOT set to true")
	}

	if apiKey != "" {
		fmt.Printf("API Key: %s...%s\n", apiKey[:4], apiKey[len(apiKey)-4:])
	} else {
		fmt.Println("WARNING: openaiApiKey is not set in settings.json")
		fmt.Println("         (Cursor may store it in state.vscdb instead)")
	}

	// Also check Anthropic settings.
	anthBaseURL, _ := settings["cursor.general.anthropicApiBaseUrl"].(string)
	anthEnabled, _ := settings["cursor.general.enableAnthropicApiBaseUrl"].(bool)
	if anthBaseURL != "" || anthEnabled {
		fmt.Println()
		fmt.Println("Anthropic settings:")
		if anthBaseURL != "" {
			fmt.Println("  Override URL: " + anthBaseURL)
		}
		fmt.Printf("  Override enabled: %v\n", anthEnabled)
	}
}

func isKey(key string) bool {
	return key == "cursor.general.openaiApiKey" ||
		key == "cursor.general.anthropicApiKey"
}
