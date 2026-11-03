package config_test

import (
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/config"
)

func TestLoadUsesDefaultsForAddrAndDB(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("BRAIN_ADDR", "")
	t.Setenv("BRAIN_DB", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.DBPath != "brain.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "brain.db")
	}
	if cfg.AdminToken != "test-admin-token" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "test-admin-token")
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("BRAIN_ADDR", "127.0.0.1:9000")
	t.Setenv("BRAIN_DB", "/data/brain.db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, "127.0.0.1:9000")
	}
	if cfg.DBPath != "/data/brain.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/data/brain.db")
	}
}

func TestLoadRequiresAdminToken(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "")
	t.Setenv("BRAIN_ADDR", "")
	t.Setenv("BRAIN_DB", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "BRAIN_ADMIN_TOKEN") {
		t.Errorf("error = %q, want mention of BRAIN_ADMIN_TOKEN", err)
	}
}

func TestLoadRejectsInvalidAddr(t *testing.T) {
	t.Setenv("BRAIN_ADMIN_TOKEN", "test-admin-token")
	t.Setenv("BRAIN_ADDR", "not-an-address")
	t.Setenv("BRAIN_DB", "")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}
