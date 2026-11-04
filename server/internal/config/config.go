package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAddr   = ":8080"
	defaultDBPath = "brain.db"
	// defaultLLMMaxInputChars matches docs/ARCHITECTURE.md §10.2.
	defaultLLMMaxInputChars = 24000
)

// LLMConfig is the OpenAI-compatible card generator configuration. Empty
// BaseURL means card generation is disabled and only heuristic cards are kept.
type LLMConfig struct {
	BaseURL       string
	APIKey        string
	Model         string
	MaxInputChars int
}

// Enabled reports whether an LLM endpoint and model are configured.
func (c LLMConfig) Enabled() bool {
	return c.BaseURL != ""
}

// Config is the server configuration loaded from the environment.
type Config struct {
	Addr       string
	DBPath     string
	AdminToken string
	LLM        LLMConfig
}

// Load reads and validates the server configuration from the environment.
func Load() (Config, error) {
	cfg := Config{
		Addr:       envOr("BRAIN_ADDR", defaultAddr),
		DBPath:     envOr("BRAIN_DB", defaultDBPath),
		AdminToken: strings.TrimSpace(os.Getenv("BRAIN_ADMIN_TOKEN")),
	}

	if cfg.AdminToken == "" {
		return Config{}, fmt.Errorf("BRAIN_ADMIN_TOKEN is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		return Config{}, fmt.Errorf("BRAIN_ADDR %q is not a valid host:port address: %w", cfg.Addr, err)
	}

	llm, err := loadLLM()
	if err != nil {
		return Config{}, err
	}
	cfg.LLM = llm

	return cfg, nil
}

func loadLLM() (LLMConfig, error) {
	llm := LLMConfig{
		BaseURL:       strings.TrimSpace(os.Getenv("BRAIN_LLM_BASE_URL")),
		APIKey:        strings.TrimSpace(os.Getenv("BRAIN_LLM_API_KEY")),
		Model:         strings.TrimSpace(os.Getenv("BRAIN_LLM_MODEL")),
		MaxInputChars: defaultLLMMaxInputChars,
	}

	if raw := strings.TrimSpace(os.Getenv("BRAIN_LLM_MAX_INPUT_CHARS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return LLMConfig{}, fmt.Errorf("BRAIN_LLM_MAX_INPUT_CHARS %q must be a positive integer", raw)
		}
		llm.MaxInputChars = value
	}

	if llm.BaseURL == "" && llm.Model == "" {
		return llm, nil
	}
	if llm.BaseURL == "" {
		return LLMConfig{}, fmt.Errorf("BRAIN_LLM_BASE_URL is required when BRAIN_LLM_MODEL is set")
	}
	if llm.Model == "" {
		return LLMConfig{}, fmt.Errorf("BRAIN_LLM_MODEL is required when BRAIN_LLM_BASE_URL is set")
	}

	parsed, err := url.Parse(llm.BaseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return LLMConfig{}, fmt.Errorf("BRAIN_LLM_BASE_URL %q must be an absolute http or https URL", llm.BaseURL)
	}
	return llm, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
