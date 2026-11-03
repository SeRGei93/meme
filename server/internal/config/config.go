package config

import (
	"fmt"
	"net"
	"os"
	"strings"
)

const (
	defaultAddr   = ":8080"
	defaultDBPath = "brain.db"
)

// Config is the server configuration loaded from the environment.
type Config struct {
	Addr       string
	DBPath     string
	AdminToken string
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

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
