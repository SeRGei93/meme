package config_test

import (
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/config"
)

func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"BRAIN_LLM_BASE_URL",
		"BRAIN_LLM_API_KEY",
		"BRAIN_LLM_MODEL",
		"BRAIN_LLM_MAX_INPUT_CHARS",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadLLMDefaults(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
	clearLLMEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LLM.Enabled() {
		t.Error("LLM.Enabled() = true, want false without BRAIN_LLM_*")
	}
	if cfg.LLM.MaxInputChars != 24000 {
		t.Errorf("LLM.MaxInputChars = %d, want 24000", cfg.LLM.MaxInputChars)
	}
}

func TestLoadReadsLLMOverrides(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
	clearLLMEnv(t)
	t.Setenv("BRAIN_LLM_BASE_URL", "https://llm.example.com/v1")
	t.Setenv("BRAIN_LLM_API_KEY", "test-key")
	t.Setenv("BRAIN_LLM_MODEL", "cheap-model")
	t.Setenv("BRAIN_LLM_MAX_INPUT_CHARS", "12000")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.LLM.Enabled() {
		t.Error("LLM.Enabled() = false, want true")
	}
	if cfg.LLM.BaseURL != "https://llm.example.com/v1" {
		t.Errorf("LLM.BaseURL = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "test-key" {
		t.Errorf("LLM.APIKey = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "cheap-model" {
		t.Errorf("LLM.Model = %q", cfg.LLM.Model)
	}
	if cfg.LLM.MaxInputChars != 12000 {
		t.Errorf("LLM.MaxInputChars = %d, want 12000", cfg.LLM.MaxInputChars)
	}
}

func TestLoadRejectsIncompleteOrInvalidLLMConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "base without model",
			env:     map[string]string{"BRAIN_LLM_BASE_URL": "https://llm.example.com/v1"},
			wantErr: "BRAIN_LLM_MODEL",
		},
		{
			name:    "model without base",
			env:     map[string]string{"BRAIN_LLM_MODEL": "cheap-model"},
			wantErr: "BRAIN_LLM_BASE_URL",
		},
		{
			name: "invalid base url",
			env: map[string]string{
				"BRAIN_LLM_BASE_URL": "not-a-url",
				"BRAIN_LLM_MODEL":    "cheap-model",
			},
			wantErr: "BRAIN_LLM_BASE_URL",
		},
		{
			name: "unsupported scheme",
			env: map[string]string{
				"BRAIN_LLM_BASE_URL": "ftp://llm.example.com",
				"BRAIN_LLM_MODEL":    "cheap-model",
			},
			wantErr: "BRAIN_LLM_BASE_URL",
		},
		{
			name: "invalid max chars",
			env: map[string]string{
				"BRAIN_LLM_BASE_URL":        "https://llm.example.com/v1",
				"BRAIN_LLM_MODEL":           "cheap-model",
				"BRAIN_LLM_MAX_INPUT_CHARS": "many",
			},
			wantErr: "BRAIN_LLM_MAX_INPUT_CHARS",
		},
		{
			name: "zero max chars",
			env: map[string]string{
				"BRAIN_LLM_BASE_URL":        "https://llm.example.com/v1",
				"BRAIN_LLM_MODEL":           "cheap-model",
				"BRAIN_LLM_MAX_INPUT_CHARS": "0",
			},
			wantErr: "BRAIN_LLM_MAX_INPUT_CHARS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
			clearLLMEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			_, err := config.Load()
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want mention of %s", err, tt.wantErr)
			}
		})
	}
}
