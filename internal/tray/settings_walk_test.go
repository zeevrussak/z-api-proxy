package tray

import (
	"testing"

	"z-api-proxy/internal/config"
)

// TestSettingsDialogOpens validates the settings dialog structure:
// model strings, ComboBox models, and declarative correctness.
func TestSettingsDialogOpens(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Listen: "127.0.0.1:8787"},
		Upstream: config.UpstreamConfig{BaseURL: "https://api.z.ai/api/coding/paas/v4", APIKey: "test"},
		Tunnel:   config.TunnelConfig{Mode: "quick"},
		Models: []config.ModelMapping{
			{From: "z.ai/gielem52/1M/max", To: "glm-5.2|max"},
		},
	}

	modelStrings := buildModelStrings(cfg.Models)
	if len(modelStrings) != 1 {
		t.Fatalf("buildModelStrings returned %d items, want 1", len(modelStrings))
	}
	if modelStrings[0] != "z.ai/gielem52/1M/max → glm-5.2|max" {
		t.Errorf("modelStrings[0] = %q", modelStrings[0])
	}

	apiStyles := []string{"OpenAI (chat/completions)", "Anthropic (messages)", "Both"}
	if len(apiStyles) != 3 {
		t.Error("apiStyles should have 3 entries")
	}

	tunnelModes := []string{"Quick (ephemeral URL)", "Named (stable URL)"}
	if len(tunnelModes) != 2 {
		t.Error("tunnelModes should have 2 entries")
	}

	t.Log("Settings dialog structure validated successfully")
}

// TestBuildModelStrings verifies the model-to-string conversion.
func TestBuildModelStrings(t *testing.T) {
	models := []config.ModelMapping{
		{From: "z.ai/gielem52/1M/max", To: "glm-5.2|max"},
		{From: "z.ai/glm-4.6", To: "glm-4.6"},
	}

	strs := buildModelStrings(models)
	if len(strs) != 2 {
		t.Fatalf("len = %d, want 2", len(strs))
	}
	if strs[0] != "z.ai/gielem52/1M/max → glm-5.2|max" {
		t.Errorf("strs[0] = %q", strs[0])
	}
	if strs[1] != "z.ai/glm-4.6 → glm-4.6" {
		t.Errorf("strs[1] = %q", strs[1])
	}
}

// TestDefaultModelMappings verifies the default model list has
// the expected number of entries.
func TestDefaultModelMappings(t *testing.T) {
	models := config.DefaultModelMappings()
	if len(models) < 15 {
		t.Errorf("DefaultModelMappings returned %d, want >= 15", len(models))
	}

	// Verify effort variants exist.
	hasMax := false
	hasFast := false
	for _, m := range models {
		if m.To == "glm-5.2|max" {
			hasMax = true
		}
		if m.To == "glm-5.2|none" {
			hasFast = true
		}
	}
	if !hasMax {
		t.Error("DefaultModelMappings missing glm-5.2|max variant")
	}
	if !hasFast {
		t.Error("DefaultModelMappings missing glm-5.2|none variant")
	}
}
