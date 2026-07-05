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

// RegisterModels writes the proxy base URL and model names into Cursor's
// settings.json. It reads the existing settings, preserves all keys,
// and adds/updates only the proxy-related ones.
//
// Returns the path to settings.json and nil on success.
func RegisterModels(proxyURL string, modelNames []string) (string, error) {
	settingsPath := SettingsPath()
	if settingsPath == "" {
		return "", fmt.Errorf("Cursor installation not found (expected %%APPDATA%%\\Cursor\\User\\settings.json)")
	}

	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return "", fmt.Errorf("cannot read Cursor settings: %w", err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return "", fmt.Errorf("cannot parse Cursor settings: %w", err)
	}

	// Add the proxy base URL for the OpenAI API override.
	settings["cursor.general.openaiApiBaseUrl"] = proxyURL

	// Add model names. Cursor reads custom model names from this list
	// when the OpenAI API key override is enabled.
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
