package cursor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

// writeSettingsFile writes raw JSON content to a fresh settings.json path
// inside a temp dir and returns the path.
func writeSettingsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write settings fixture: %v", err)
	}
	return path
}

// readSettings reads and json-decodes a settings.json file.
func readSettings(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]interface{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	return settings
}

// toStringSlice converts a decoded JSON []interface{} of strings into
// a []string for easier assertions.
func toStringSlice(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// newStateDBFixture creates a fresh state.vscdb-shaped SQLite file at
// dbPath with an ItemTable. If includeRow is true, it inserts a row for
// appUserKey with the given raw JSON value (use "" for an empty-but-present
// row, or includeRow=false to simulate a DB where the key is entirely
// absent).
func newStateDBFixture(t *testing.T, dbPath string, appUserJSON string, includeRow bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create ItemTable: %v", err)
	}
	if includeRow {
		if _, err := db.Exec(`INSERT INTO ItemTable (key, value) VALUES (?, ?)`, appUserKey, appUserJSON); err != nil {
			t.Fatalf("insert appUserKey row: %v", err)
		}
	}
}

// readAppUserData opens dbPath and returns the decoded applicationUser blob.
func readAppUserData(t *testing.T, dbPath string) map[string]interface{} {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var raw string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = ?`, appUserKey).Scan(&raw); err != nil {
		t.Fatalf("read back applicationUser row: %v", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal applicationUser: %v", err)
	}
	return data
}

// ---------------------------------------------------------------------
// writeSettingsJSON
// ---------------------------------------------------------------------

func TestWriteSettingsJSON_MergesIntoExistingFile(t *testing.T) {
	path := writeSettingsFile(t, `{"editor.fontSize": 14}`)

	err := writeSettingsJSON(path, "http://127.0.0.1:8787/v1", []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}, "mycursorkey", "myclientid")
	if err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	settings := readSettings(t, path)

	if got := settings["cursor.general.openaiApiBaseUrl"]; got != "http://127.0.0.1:8787/v1" {
		t.Errorf("openaiApiBaseUrl = %v, want http://127.0.0.1:8787/v1", got)
	}
	if got, _ := settings["cursor.general.enableOpenaiApiBaseUrl"].(bool); got != true {
		t.Errorf("enableOpenaiApiBaseUrl = %v, want true", settings["cursor.general.enableOpenaiApiBaseUrl"])
	}
	// Composite key format is cursorKey + "_" + clientID.
	if got := settings["cursor.general.openaiApiKey"]; got != "mycursorkey_myclientid" {
		t.Errorf("openaiApiKey = %v, want mycursorkey_myclientid", got)
	}
	models := toStringSlice(settings["cursor.general.modelNames"])
	if !containsStr(models, "z.ai/glm-5.2") || !containsStr(models, "z.ai/glm-4.6") {
		t.Errorf("modelNames = %v, want to contain both new models", models)
	}
	// Proves this is a merge, not an overwrite: pre-existing unrelated key survives.
	if got, ok := settings["editor.fontSize"]; !ok || got != float64(14) {
		t.Errorf("editor.fontSize = %v (ok=%v), want 14 (untouched)", got, ok)
	}
}

func TestWriteSettingsJSON_EmptyCursorKey_KeyNotSetWhenAbsent(t *testing.T) {
	path := writeSettingsFile(t, `{}`)

	if err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "", "someclientid"); err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	settings := readSettings(t, path)
	if _, ok := settings["cursor.general.openaiApiKey"]; ok {
		t.Errorf("openaiApiKey should not be set when cursorKey is empty, got %v", settings["cursor.general.openaiApiKey"])
	}
}

func TestWriteSettingsJSON_EmptyCursorKey_PreservesExistingKey(t *testing.T) {
	// If cursorKey is empty, the `if cursorKey != ""` guard skips setting
	// openaiApiKey entirely, so whatever was already on disk survives.
	path := writeSettingsFile(t, `{"cursor.general.openaiApiKey": "old-value"}`)

	if err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "", ""); err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	settings := readSettings(t, path)
	if got := settings["cursor.general.openaiApiKey"]; got != "old-value" {
		t.Errorf("openaiApiKey = %v, want old-value to be preserved", got)
	}
}

func TestWriteSettingsJSON_EmptyClientID_NoSuffix(t *testing.T) {
	path := writeSettingsFile(t, `{}`)

	if err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "mycursorkey", ""); err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	settings := readSettings(t, path)
	if got := settings["cursor.general.openaiApiKey"]; got != "mycursorkey" {
		t.Errorf("openaiApiKey = %v, want mycursorkey (no _ suffix)", got)
	}
}

func TestWriteSettingsJSON_DedupsModelNames(t *testing.T) {
	path := writeSettingsFile(t, `{"cursor.general.modelNames": ["z.ai/glm-5.2", "other-model"]}`)

	if err := writeSettingsJSON(path, "http://proxy/v1", []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}, "", ""); err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	settings := readSettings(t, path)
	models := toStringSlice(settings["cursor.general.modelNames"])
	if len(models) != 3 {
		t.Fatalf("modelNames = %v, want 3 entries (no duplicate of z.ai/glm-5.2)", models)
	}
	seen := map[string]int{}
	for _, m := range models {
		seen[m]++
	}
	for name, count := range seen {
		if count != 1 {
			t.Errorf("model %q appears %d times, want 1", name, count)
		}
	}
	if !containsStr(models, "other-model") || !containsStr(models, "z.ai/glm-5.2") || !containsStr(models, "z.ai/glm-4.6") {
		t.Errorf("modelNames = %v, missing an expected entry", models)
	}
}

// TestWriteSettingsJSON_FilePermissions is a regression guard for the
// security fix that made settings.json (which now contains a plaintext
// composite API key) non-world/group-readable.
//
// EMPIRICAL FINDING (verified on this Windows/NTFS machine via a
// throwaway experiment calling os.WriteFile with modes 0600, 0644, 0666,
// and 0000 and then os.Stat-ing the result):
//
//	os.WriteFile(path, data, 0600) -> os.Stat(...).Mode().Perm() == 0666
//	os.WriteFile(path, data, 0644) -> os.Stat(...).Mode().Perm() == 0666
//	os.WriteFile(path, data, 0666) -> os.Stat(...).Mode().Perm() == 0666
//	os.WriteFile(path, data, 0000) -> os.Stat(...).Mode().Perm() == 0444
//
// Go's os.WriteFile on Windows maps the POSIX mode to the NTFS
// "read-only" file attribute only: ANY mode with a write bit set (0600,
// 0644, 0666, ...) clears the read-only attribute and reports back as
// 0666 regardless of which specific mode was requested; only a mode with
// NO write bits at all (0000) sets the read-only attribute (reported as
// 0444). There is therefore no way — via os.Stat().Mode().Perm() on this
// platform — to distinguish "written with 0600" from "written with
// 0644" from "written with 0666"; a strict `== 0600` assertion would
// always fail here, and a differential 0600-vs-0644 comparison would
// always report them as identical (not narrower).
//
// Given that, the assertion below verifies the one thing that IS
// meaningful and observable on Windows: the file was written in a
// normal writable state (not accidentally read-only, which is the
// failure mode a mode-bit typo like 0000 would actually produce here).
// True per-user access restriction on Windows would require NTFS ACL
// manipulation (golang.org/x/sys/windows or icacls), which this code
// does not do — see the final report for this caveat.
func TestWriteSettingsJSON_FilePermissions(t *testing.T) {
	path := writeSettingsFile(t, `{}`)

	if err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "key", ""); err != nil {
		t.Fatalf("writeSettingsJSON: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0666 {
		t.Errorf("Mode().Perm() = %o, want 0666 (observed Windows behavior for any WriteFile mode with a write bit set, incl. the intended 0600) — file may have been left in an unexpected read-only state", perm)
	}
}

func TestWriteSettingsJSON_MalformedJSON_ReturnsErrorAndLeavesFileIntact(t *testing.T) {
	original := `{not valid json`
	path := writeSettingsFile(t, original)

	err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "key", "")
	if err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read back settings after failed write: %v", readErr)
	}
	if string(raw) != original {
		t.Errorf("file content changed after failed writeSettingsJSON: got %q, want unchanged %q", raw, original)
	}
}

func TestWriteSettingsJSON_NonexistentPath_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	err := writeSettingsJSON(path, "http://proxy/v1", []string{"m1"}, "key", "")
	if err == nil {
		t.Fatal("expected an error for a nonexistent settings path, got nil")
	}
}

// ---------------------------------------------------------------------
// writeStateDB
// ---------------------------------------------------------------------

func TestWriteStateDB_PopulatesAllThreeLocations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.vscdb")
	initial := `{"aiSettings":{"userAddedModels":["existing-model"]}}`
	newStateDBFixture(t, dbPath, initial, true)

	if err := writeStateDB(dbPath, []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}); err != nil {
		t.Fatalf("writeStateDB: %v", err)
	}

	data := readAppUserData(t, dbPath)

	aiSettings, ok := data["aiSettings"].(map[string]interface{})
	if !ok {
		t.Fatalf("aiSettings missing or wrong type: %v", data["aiSettings"])
	}

	userAdded := toStringSlice(aiSettings["userAddedModels"])
	for _, want := range []string{"existing-model", "z.ai/glm-5.2", "z.ai/glm-4.6"} {
		if !containsStr(userAdded, want) {
			t.Errorf("userAddedModels = %v, missing %q", userAdded, want)
		}
	}

	overrideEnabled := toStringSlice(aiSettings["modelOverrideEnabled"])
	for _, want := range []string{"z.ai/glm-5.2", "z.ai/glm-4.6"} {
		if !containsStr(overrideEnabled, want) {
			t.Errorf("modelOverrideEnabled = %v, missing %q", overrideEnabled, want)
		}
	}

	avail, ok := data["availableDefaultModels2"].([]interface{})
	if !ok {
		t.Fatalf("availableDefaultModels2 missing or wrong type: %v", data["availableDefaultModels2"])
	}
	found := map[string]map[string]interface{}{}
	for _, m := range avail {
		obj, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := obj["name"].(string); ok {
			found[name] = obj
		}
	}
	for _, name := range []string{"z.ai/glm-5.2", "z.ai/glm-4.6"} {
		obj, ok := found[name]
		if !ok {
			t.Fatalf("availableDefaultModels2 missing object for %q: %v", name, avail)
		}
		if supportsAgent, _ := obj["supportsAgent"].(bool); !supportsAgent {
			t.Errorf("model %q supportsAgent = %v, want true", name, obj["supportsAgent"])
		}
		tagline, _ := obj["tagline"].(string)
		if tagline == "" || !containsSubstr(tagline, "z.ai") {
			t.Errorf("model %q tagline = %q, want to contain z.ai", name, tagline)
		}
	}
}

func containsSubstr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestWriteStateDB_SecondCallWithOverlap_NoDuplicates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.vscdb")
	newStateDBFixture(t, dbPath, `{}`, true)

	if err := writeStateDB(dbPath, []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}); err != nil {
		t.Fatalf("writeStateDB (first call): %v", err)
	}
	// Overlapping call: one repeated model, one new.
	if err := writeStateDB(dbPath, []string{"z.ai/glm-5.2", "z.ai/glm-9.9"}); err != nil {
		t.Fatalf("writeStateDB (second call): %v", err)
	}

	data := readAppUserData(t, dbPath)
	aiSettings := data["aiSettings"].(map[string]interface{})

	assertNoDuplicates(t, "userAddedModels", toStringSlice(aiSettings["userAddedModels"]))
	assertNoDuplicates(t, "modelOverrideEnabled", toStringSlice(aiSettings["modelOverrideEnabled"]))

	avail := data["availableDefaultModels2"].([]interface{})
	nameCounts := map[string]int{}
	for _, m := range avail {
		obj := m.(map[string]interface{})
		nameCounts[obj["name"].(string)]++
	}
	for name, count := range nameCounts {
		if count != 1 {
			t.Errorf("availableDefaultModels2 has %d entries for %q, want 1", count, name)
		}
	}
	for _, want := range []string{"z.ai/glm-5.2", "z.ai/glm-4.6", "z.ai/glm-9.9"} {
		if nameCounts[want] != 1 {
			t.Errorf("expected exactly one availableDefaultModels2 entry for %q, got %d", want, nameCounts[want])
		}
	}
}

func assertNoDuplicates(t *testing.T, field string, list []string) {
	t.Helper()
	seen := map[string]int{}
	for _, v := range list {
		seen[v]++
	}
	for v, count := range seen {
		if count != 1 {
			t.Errorf("%s: %q appears %d times, want 1 (list=%v)", field, v, count, list)
		}
	}
}

func TestWriteStateDB_MissingAppUserKeyRow_ReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.vscdb")
	newStateDBFixture(t, dbPath, "", false) // table exists, row doesn't.

	err := writeStateDB(dbPath, []string{"z.ai/glm-5.2"})
	if err == nil {
		t.Fatal("expected an error when appUserKey row is missing, got nil")
	}
}

func TestWriteStateDB_NonexistentDBFile_ReturnsError(t *testing.T) {
	// sql.Open with the sqlite driver is lazy: it does not touch the
	// filesystem until the first query. modernc.org/sqlite auto-creates
	// an empty DB file on first access, so the failure surfaces from the
	// SELECT against a table that doesn't exist yet (verified empirically
	// by running this test and inspecting the error below).
	dbPath := filepath.Join(t.TempDir(), "state.vscdb")

	err := writeStateDB(dbPath, []string{"z.ai/glm-5.2"})
	if err == nil {
		t.Fatal("expected an error for a state.vscdb that doesn't exist yet, got nil")
	}
	t.Logf("observed error for nonexistent db file: %v", err)
}

// ---------------------------------------------------------------------
// VerifyModels
// ---------------------------------------------------------------------

// setupCursorAppData points APPDATA at a fresh temp dir and creates the
// Cursor/User/globalStorage directory structure StateDBPath() expects.
// Returns the path where state.vscdb should be placed.
func setupCursorAppData(t *testing.T) string {
	t.Helper()
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)
	dir := filepath.Join(appData, "Cursor", "User", "globalStorage")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir globalStorage: %v", err)
	}
	return filepath.Join(dir, "state.vscdb")
}

func TestVerifyModels_AllPresent(t *testing.T) {
	dbPath := setupCursorAppData(t)
	newStateDBFixture(t, dbPath, `{}`, true)
	if err := writeStateDB(dbPath, []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}); err != nil {
		t.Fatalf("fixture writeStateDB: %v", err)
	}

	missing, err := VerifyModels([]string{"z.ai/glm-5.2", "z.ai/glm-4.6"})
	if err != nil {
		t.Fatalf("VerifyModels: unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %v, want empty", missing)
	}
}

func TestVerifyModels_SomeMissing(t *testing.T) {
	dbPath := setupCursorAppData(t)
	newStateDBFixture(t, dbPath, `{}`, true)
	if err := writeStateDB(dbPath, []string{"z.ai/glm-5.2"}); err != nil {
		t.Fatalf("fixture writeStateDB: %v", err)
	}

	missing, err := VerifyModels([]string{"z.ai/glm-5.2", "z.ai/glm-4.6", "z.ai/glm-4.5"})
	if err != nil {
		t.Fatalf("VerifyModels: unexpected error: %v", err)
	}
	want := []string{"z.ai/glm-4.6", "z.ai/glm-4.5"}
	if len(missing) != len(want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
	for i, w := range want {
		if missing[i] != w {
			t.Errorf("missing[%d] = %q, want %q (input-order preserved)", i, missing[i], w)
		}
	}
}

func TestVerifyModels_NoStateDB_ReturnsFullListAndError(t *testing.T) {
	// APPDATA points somewhere with no Cursor install at all: StateDBPath()
	// returns "" because os.Stat on state.vscdb fails.
	t.Setenv("APPDATA", t.TempDir())

	input := []string{"z.ai/glm-5.2", "z.ai/glm-4.6"}
	missing, err := VerifyModels(input)
	if err == nil {
		t.Fatal("expected an error when state.vscdb is not found, got nil")
	}
	if len(missing) != len(input) {
		t.Fatalf("missing = %v, want the full input list %v", missing, input)
	}
	for i, w := range input {
		if missing[i] != w {
			t.Errorf("missing[%d] = %q, want %q", i, missing[i], w)
		}
	}
}
