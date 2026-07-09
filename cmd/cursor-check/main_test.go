package main

import "testing"

func TestIsKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{"openai key", "cursor.general.openaiApiKey", true},
		{"anthropic key", "cursor.general.anthropicApiKey", true},
		{"unrelated key", "cursor.general.openaiApiBaseUrl", false},
		{"typo'd key name", "cursor.general.openaiApiKeyy", false},
		{"substring of key", "openaiApiKey", false},
		{"empty string", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKey(tt.key); got != tt.want {
				t.Errorf("isKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
