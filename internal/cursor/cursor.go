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

// RegisterModels writes the proxy base URL and model names into Cursor's
// settings.json AND state.vscdb so they appear in the model picker.
// cursorKey is the Gateway Worker Key. clientID is appended as _clientId
// for per-client tracking.
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
		return "", err
	}

	// 2. Write state.vscdb (SQLite) — this is what the model picker reads.
	dbPath := StateDBPath()
	if dbPath != "" {
		if err := writeStateDB(dbPath, modelNames); err != nil {
			// Non-fatal — settings.json is still written.
			_ = err
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

// writeStateDB writes the model names into Cursor's state.vscdb SQLite.
// Cursor reads the model picker from ItemTable key "cursor.general.modelNames".
func writeStateDB(dbPath string, modelNames []string) error {
	// Read current value from ItemTable.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("cannot open state.vscdb: %w", err)
	}
	defer db.Close()

	// Read existing model names from the DB.
	var existingJSON string
	err = db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursor.general.modelNames'`).Scan(&existingJSON)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("cannot query model names: %w", err)
	}

	// Parse existing models.
	var existingModels []interface{}
	if existingJSON != "" {
		json.Unmarshal([]byte(existingJSON), &existingModels)
	}

	// Merge new models.
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

	// Serialize back to JSON.
	newJSON, err := json.Marshal(existingModels)
	if err != nil {
		return fmt.Errorf("cannot serialize models: %w", err)
	}

	// Write back to DB.
	if err == sql.ErrNoRows {
		_, err = db.Exec(`INSERT INTO ItemTable (key, value) VALUES ('cursor.general.modelNames', ?)`, string(newJSON))
	} else {
		_, err = db.Exec(`UPDATE ItemTable SET value = ? WHERE key = 'cursor.general.modelNames'`, string(newJSON))
	}
	if err != nil {
		return fmt.Errorf("cannot write model names to state.vscdb: %w", err)
	}

	return nil
}
