package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/SeRGei93/meme/server/internal/card"
	"github.com/SeRGei93/meme/server/internal/config"
)

func TestNewCardGeneratorWiresTheLLMWhenConfigured(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	disabled, err := newCardGenerator(config.Config{}, nil, logger)
	if err != nil {
		t.Fatalf("newCardGenerator(disabled) error = %v", err)
	}
	generator, ok := disabled.(*card.Generator)
	if !ok {
		t.Fatalf("newCardGenerator() = %T, want *card.Generator", disabled)
	}
	if generator.Client != nil {
		t.Error("disabled generator has an LLM client, want fallback-only")
	}

	enabled, err := newCardGenerator(config.Config{LLM: config.LLMConfig{
		BaseURL:       "https://llm.example.com/v1",
		APIKey:        "test-key",
		Model:         "cheap-model",
		MaxInputChars: 12000,
	}}, nil, logger)
	if err != nil {
		t.Fatalf("newCardGenerator(enabled) error = %v", err)
	}
	generator, ok = enabled.(*card.Generator)
	if !ok {
		t.Fatalf("newCardGenerator() = %T, want *card.Generator", enabled)
	}
	if generator.Client == nil {
		t.Error("enabled generator has no LLM client")
	}
	if generator.Model != "cheap-model" {
		t.Errorf("Model = %q, want cheap-model", generator.Model)
	}
	if generator.MaxInputChars != 12000 {
		t.Errorf("MaxInputChars = %d, want 12000", generator.MaxInputChars)
	}
}
