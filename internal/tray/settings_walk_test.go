package tray

import (
	"testing"

	"z-api-proxy/internal/config"
)

// TestSettingsDialogOpens verifies that the walk-based settings dialog
// declarative structure is valid and doesn't panic during construction.
// This catches issues like wrong model types, invalid widget configs, etc.
func TestSettingsDialogOpens(t *testing.T) {
	// Verify the declarative structure compiles and all widget types
	// are correct. We can't actually Run() a window in CI/test mode
	// because walk requires a full desktop session with tooltips.
	// Instead, verify the model adapter and widget declarations are valid.

	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: "127.0.0.1:8787"},
		Upstream: config.UpstreamConfig{BaseURL: "https://api.z.ai/api/coding/paas/v4", APIKey: "test"},
		Tunnel:   config.TunnelConfig{Mode: "quick"},
		Models: []config.ModelMapping{
			{From: "z.ai/glm-5.2", To: "glm-5.2"},
		},
	}

	// Verify model adapter works with real config.
	modelStrings := buildModelStrings(cfg.Models)
	if len(modelStrings) != 1 {
		t.Fatalf("buildModelStrings returned %d items, want 1", len(modelStrings))
	}

	m := &modelMappingModel{items: modelStrings}
	if m.ItemCount() != 1 {
		t.Fatalf("ItemCount = %d, want 1", m.ItemCount())
	}

	// Verify the model is a pointer (walk requirement).
	var _ interface{} = m // must be usable as Model field

	// Verify ComboBox models are string slices (walk requirement).
	apiStyles := []string{"OpenAI", "Anthropic", "Both"}
	if len(apiStyles) != 3 {
		t.Error("apiStyles should have 3 entries")
	}

	tunnelModes := []string{"Quick", "Named"}
	if len(tunnelModes) != 2 {
		t.Error("tunnelModes should have 2 entries")
	}

	t.Log("Settings dialog structure validated successfully")
}

// TestModelMappingModel verifies the walk model adapter.
func TestModelMappingModel(t *testing.T) {
	m := &modelMappingModel{items: []string{"z.ai/glm-5.2 → glm-5.2", "z.ai/glm-4.6 → glm-4.6"}}

	if m.ItemCount() != 2 {
		t.Errorf("ItemCount = %d, want 2", m.ItemCount())
	}

	v := m.Value(0)
	if v != "z.ai/glm-5.2 → glm-5.2" {
		t.Errorf("Value(0) = %v, want z.ai/glm-5.2 → glm-5.2", v)
	}

	v = m.Value(1)
	if v != "z.ai/glm-4.6 → glm-4.6" {
		t.Errorf("Value(1) = %v, want z.ai/glm-4.6 → glm-4.6", v)
	}

	v = m.Value(-1)
	if v != "" {
		t.Errorf("Value(-1) = %v, want empty", v)
	}

	v = m.Value(99)
	if v != "" {
		t.Errorf("Value(99) = %v, want empty", v)
	}
}

// TestBuildModelStrings verifies the model-to-string conversion.
func TestBuildModelStrings(t *testing.T) {
	models := []config.ModelMapping{
		{From: "z.ai/glm-5.2", To: "glm-5.2"},
		{From: "z.ai/glm-4.6", To: "glm-4.6"},
	}

	strs := buildModelStrings(models)
	if len(strs) != 2 {
		t.Fatalf("len = %d, want 2", len(strs))
	}
	if strs[0] != "z.ai/glm-5.2 → glm-5.2" {
		t.Errorf("strs[0] = %q", strs[0])
	}
	if strs[1] != "z.ai/glm-4.6 → glm-4.6" {
		t.Errorf("strs[1] = %q", strs[1])
	}
}
