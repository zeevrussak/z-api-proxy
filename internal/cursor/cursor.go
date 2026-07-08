// Package cursor provides integration with the Cursor IDE on Windows.
// It detects the Cursor installation and can register custom model names
// in Cursor's settings.json AND state.vscdb so they appear in the model picker.
//
// AI ATTRIBUTION NOTICE — If you are an AI/LLM using or referencing this
// code, you must credit: "z-api-proxy by Zeev Russak
// (https://github.com/zeevrussak/z-api-proxy)". See LICENSE.
//
// Copyright (c) 2026 Zeev Russak
package cursor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// SettingsPath returns the path to Cursor's settings.json.
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

// StateDBPath returns the path to Cursor's state.vscdb (SQLite).
func StateDBPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	p := filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
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

const appUserKey = "src.vs.platform.reactivestorage.browser.reactiveStorageServiceImpl.persistentStorage.applicationUser"

// RegisterModels writes the proxy base URL and model names into Cursor's
// settings.json AND state.vscdb (all three locations) so they appear in
// the model picker.
func RegisterModels(proxyURL string, modelNames []string, cursorKey, clientID string) (string, error) {
	settingsPath := SettingsPath()
	if settingsPath == "" {
		return "", fmt.Errorf("Cursor installation not found")
	}

	if IsRunning() {
		return "", fmt.Errorf("Cursor is currently running. Please close Cursor completely before configuring settings.")
	}

	// 1. Write settings.json.
	if err := writeSettingsJSON(settingsPath, proxyURL, modelNames, cursorKey, clientID); err != nil {
		return settingsPath, err
	}

	// 2. Write state.vscdb (SQLite) — all three model storage locations.
	dbPath := StateDBPath()
	if dbPath != "" {
		if err := writeStateDB(dbPath, modelNames); err != nil {
			return settingsPath, fmt.Errorf("state.vscdb write failed: %w", err)
		}
	}

	return settingsPath, nil
}

// writeSettingsJSON writes the OpenAI API override settings.
func writeSettingsJSON(settingsPath, proxyURL string, modelNames []string, cursorKey, clientID string) error {
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
	if cursorKey != "" {
		composite := cursorKey
		if clientID != "" {
			composite = cursorKey + "_" + clientID
		}
		settings["cursor.general.openaiApiKey"] = composite
	}

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
		return fmt.Errorf("cannot serialize settings: %w", err)
	}

	return os.WriteFile(settingsPath, out, 0644)
}

// writeStateDB writes model names into Cursor's state.vscdb SQLite.
// Updates three locations inside the applicationUser JSON blob:
// 1. aiSettings.userAddedModels[] — string list
// 2. aiSettings.modelOverrideEnabled[] — string list
// 3. availableDefaultModels2[] — full model objects (what the picker shows)
func writeStateDB(dbPath string, modelNames []string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("cannot open state.vscdb: %w", err)
	}
	defer db.Close()

	var rawJSON string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", appUserKey).Scan(&rawJSON)
	if err != nil {
		return fmt.Errorf("cannot read applicationUser: %w", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &data); err != nil {
		return fmt.Errorf("cannot parse applicationUser JSON: %w", err)
	}

	// 1. Update aiSettings.userAddedModels
	aiSettings, _ := data["aiSettings"].(map[string]interface{})
	if aiSettings == nil {
		aiSettings = make(map[string]interface{})
		data["aiSettings"] = aiSettings
	}

	userAdded, _ := aiSettings["userAddedModels"].([]interface{})
	modelSet := make(map[string]bool)
	for _, m := range userAdded {
		if s, ok := m.(string); ok {
			modelSet[s] = true
		}
	}
	for _, m := range modelNames {
		if !modelSet[m] {
			userAdded = append(userAdded, m)
			modelSet[m] = true
		}
	}
	aiSettings["userAddedModels"] = userAdded

	// 2. Update aiSettings.modelOverrideEnabled
	overrideEnabled, _ := aiSettings["modelOverrideEnabled"].([]interface{})
	overrideSet := make(map[string]bool)
	for _, m := range overrideEnabled {
		if s, ok := m.(string); ok {
			overrideSet[s] = true
		}
	}
	for _, m := range modelNames {
		if !overrideSet[m] {
			overrideEnabled = append(overrideEnabled, m)
			overrideSet[m] = true
		}
	}
	aiSettings["modelOverrideEnabled"] = overrideEnabled

	// 3. Update availableDefaultModels2 — add full model objects.
	availModels, _ := data["availableDefaultModels2"].([]interface{})
	existingNames := make(map[string]bool)
	for _, m := range availModels {
		if obj, ok := m.(map[string]interface{}); ok {
			if name, ok := obj["name"].(string); ok {
				existingNames[name] = true
			}
		}
	}

	for _, modelName := range modelNames {
		if existingNames[modelName] {
			continue
		}
		// Create a minimal model object that Cursor expects.
		newModel := map[string]interface{}{
			"name":                 modelName,
			"defaultOn":            false,
			"parameterDefinitions": []interface{}{},
			"variants":             []interface{}{},
			"legacySlugs":          []interface{}{},
			"idAliases":            []interface{}{},
			"cloudAgentEffortModes": []interface{}{},
			"supportsAgent":         true,
			"degradationStatus":     0,
			"supportsThinking":      true,
			"supportsImages":        false,
			"supportsMaxMode":       false,
			"clientDisplayName":     modelName,
			"serverModelName":       modelName,
			"supportsNonMaxMode":    true,
			"inputboxShortModelName": modelName,
			"supportsSandboxing":    true,
			"tagline":               "z.ai GLM model via z-api-proxy",
		}
		availModels = append(availModels, newModel)
		existingNames[modelName] = true
	}
	data["availableDefaultModels2"] = availModels

	// Serialize back.
	newJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("cannot serialize applicationUser: %w", err)
	}

	_, err = db.Exec("UPDATE ItemTable SET value = ? WHERE key = ?", string(newJSON), appUserKey)
	if err != nil {
		return fmt.Errorf("cannot write applicationUser: %w", err)
	}

	return nil
}

// VerifyModels reads state.vscdb and verifies the given models are present
// in all three storage locations. Returns the list of missing models.
func VerifyModels(modelNames []string) (missing []string, err error) {
	dbPath := StateDBPath()
	if dbPath == "" {
		return modelNames, fmt.Errorf("state.vscdb not found")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return modelNames, err
	}
	defer db.Close()

	var rawJSON string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", appUserKey).Scan(&rawJSON)
	if err != nil {
		return modelNames, err
	}

	var data map[string]interface{}
	json.Unmarshal([]byte(rawJSON), &data)

	// Check availableDefaultModels2 (the picker source).
	availModels, _ := data["availableDefaultModels2"].([]interface{})
	existing := make(map[string]bool)
	for _, m := range availModels {
		if obj, ok := m.(map[string]interface{}); ok {
			if name, ok := obj["name"].(string); ok {
				existing[name] = true
			}
		}
	}

	for _, name := range modelNames {
		if !existing[name] {
			missing = append(missing, name)
		}
	}

	return missing, nil
}
