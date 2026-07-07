// Package cursor provides integration with the Cursor IDE on Windows.
// It detects the Cursor installation and can register custom model names
// in Cursor's settings so they appear in the model picker.
//
// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package cursor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SettingsPath returns the path to Cursor's settings.json, or empty
// string if Cursor is not installed.
func SettingsPath() string {
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

// IsInstalled reports whether Cursor appears to be installed.
func IsInstalled() bool {
	return SettingsPath() != ""
}

// IsRunning reports whether the Cursor process is currently running.
// Writing settings.json while Cursor is running causes Cursor to
// overwrite the file on exit with its in-memory state.
func IsRunning() bool {
	cmd := exec.Command("tasklist", "/FI", "IMAGENAME eq Cursor.exe", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(out) > 0 && contains(string(out), "Cursor.exe")
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// RegisterModels writes the proxy base URL and model names into Cursor's
// settings.json using the OpenAI API override.
// cursorKey is the Gateway Worker Key. clientID is appended as _clientId
// for per-client tracking. Returns the path and nil on success.
func RegisterModels(proxyURL string, modelNames []string, cursorKey, clientID string) (string, error) {
	settingsPath := SettingsPath()
	if settingsPath == "" {
		return "", fmt.Errorf("Cursor installation not found (expected %%APPDATA%%\\Cursor\\User\\settings.json)")
	}

	if IsRunning() {
		return "", fmt.Errorf("Cursor is currently running. Please close Cursor completely before configuring settings — otherwise Cursor will overwrite the changes on exit.")
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return "", fmt.Errorf("cannot read Cursor settings: %w", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", fmt.Errorf("cannot parse Cursor settings: %w", err)
	}

	// Configure OpenAI API override (the only API Cursor supports for custom models).
	settings["cursor.general.openaiApiBaseUrl"] = proxyURL
	settings["cursor.general.enableOpenaiApiBaseUrl"] = true
	if cursorKey != "" {
		composite := cursorKey
		if clientID != "" {
			composite = cursorKey + "_" + clientID
		}
		settings["cursor.general.openaiApiKey"] = composite
	}

	// Add model names.
	existingModels, _ := settings["cursor.general.modelNames"].([]interface{})
	modelSet := make(map[string]bool)
	for _, m := range existingModels {
		if s, ok := m.(string); ok {
			modelSet[s] = true
		}
	}
	for _, m := range modelNames {
		if !modelSet[m] {
			existingModels = append(existingModels, m)
		}
	}
	settings["cursor.general.modelNames"] = existingModels

	out, err := json.MarshalIndent(settings, "", "    ")
	if err != nil {
		return "", fmt.Errorf("cannot serialize settings: %w", err)
	}

	if err := os.WriteFile(settingsPath, out, 0644); err != nil {
		return "", fmt.Errorf("cannot write Cursor settings: %w", err)
	}

	return settingsPath, nil
}
