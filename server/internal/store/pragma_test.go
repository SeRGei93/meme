package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/SeRGei93/meme/server/internal/auth"
)

func TestOpenConfiguresSQLiteForSingleWriter(t *testing.T) {
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	tests := []struct {
		pragma string
		want   any
	}{
		{"PRAGMA journal_mode", "wal"},
		{"PRAGMA foreign_keys", int64(1)},
		{"PRAGMA busy_timeout", int64(5000)},
		{"PRAGMA synchronous", int64(1)},
	}
	for _, tt := range tests {
		var got any
		switch tt.want.(type) {
		case string:
			var value string
			if err := st.db.QueryRow(tt.pragma).Scan(&value); err != nil {
				t.Fatalf("%s: %v", tt.pragma, err)
			}
			got = value
		case int64:
			var value int64
			if err := st.db.QueryRow(tt.pragma).Scan(&value); err != nil {
				t.Fatalf("%s: %v", tt.pragma, err)
			}
			got = value
		}
		if got != tt.want {
			t.Errorf("%s = %v, want %v", tt.pragma, got, tt.want)
		}
	}
}

func TestFindClientByTokenHashExcludesRevokedClients(t *testing.T) {
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	client, token, err := st.CreateClient(context.Background(), "revoked", auth.KindUI, []auth.Scope{auth.ScopeUI})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := st.db.Exec(`UPDATE clients SET revoked_at = '2026-11-03T12:00:00Z' WHERE id = ?`, client.ID); err != nil {
		t.Fatalf("revoke fixture: %v", err)
	}

	_, err = st.FindClientByTokenHash(context.Background(), auth.HashToken(token))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindClientByTokenHash() error = %v, want ErrNotFound", err)
	}
}

func TestHealthFailsWhenDatabaseIsReadOnly(t *testing.T) {
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	if _, err := st.db.Exec("PRAGMA query_only = ON"); err != nil {
		t.Fatalf("enable query_only: %v", err)
	}
	if _, err := st.Health(context.Background()); err == nil {
		t.Fatal("Health() error = nil, want error for a read-only database")
	}
}

func TestHealthDoesNotLeaveProbeTableBehind(t *testing.T) {
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	for i := 0; i < 2; i++ {
		if _, err := st.Health(context.Background()); err != nil {
			t.Fatalf("Health() call %d error = %v", i+1, err)
		}
	}

	var count int
	if err := st.db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'health_write_probe'",
	).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 0 {
		t.Errorf("health_write_probe tables = %d, want 0", count)
	}
}

func TestClientsTableStoresTokenHashOnly(t *testing.T) {
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	_, token, err := st.CreateClient(context.Background(), "hashed", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	var stored string
	if err := st.db.QueryRow(`SELECT token_hash FROM clients WHERE name = 'hashed'`).Scan(&stored); err != nil {
		t.Fatalf("read token_hash: %v", err)
	}
	if stored == token {
		t.Fatal("plaintext token was stored in token_hash")
	}
	if stored != auth.HashToken(token) {
		t.Errorf("stored token_hash = %q, want SHA-256 of the token", stored)
	}
}
