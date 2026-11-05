package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// The generic-clients migration rebuilds the clients table, backfills each
// record's source from its client and must leave foreign keys valid.
func TestOpenMigratesV3DatabaseToGenericClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	db, err := sql.Open(driverName, "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	apply := func(name string) {
		t.Helper()
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
	apply("0001_clients.sql")
	apply("0002_records.sql")
	apply("0003_memory_idempotency.sql")
	if _, err := db.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
		INSERT INTO schema_migrations (version, applied_at) VALUES
			(1, '2026-11-04T12:00:00Z'), (2, '2026-11-04T12:00:00Z'), (3, '2026-11-04T12:00:00Z')`); err != nil {
		t.Fatalf("record schema versions: %v", err)
	}

	now := "2026-11-04T12:00:00Z"
	clients := []struct {
		id      int64
		name    string
		kind    string
		revoked bool
	}{
		{1, "opencode-macbook", "opencode", false},
		{2, "jarvis", "jarvis", false},
		{3, "second-brain-app", "ui", false},
		{4, "OpenCode:MacBook", "opencode", false},
		{5, "foo-1", "opencode", false},
		{6, "Foo", "opencode", false},
		{7, "foo", "opencode", false},
		{8, "jarvis", "jarvis", true},
		{9, "foo-7", "opencode", false},
	}
	for _, client := range clients {
		var revokedAt any
		if client.revoked {
			revokedAt = now
		}
		if _, err := db.Exec(`
			INSERT INTO clients (id, name, kind, token_hash, scopes, created_at, revoked_at)
			VALUES (?, ?, ?, ?, 'ingest', ?, ?)`,
			client.id, client.name, client.kind, fmt.Sprintf("hash-%d", client.id), now, revokedAt,
		); err != nil {
			t.Fatalf("insert client %s: %v", client.name, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO records (id, client_id, source, external_id, title, raw_text, content_hash, updated_at, indexed_at)
		VALUES
			(1, 1, 'opencode', 'ses_1', 'Session', 'raw', 'hash-1', ?, ?),
			(2, 2, 'jarvis', 'evt_1', 'Memory', 'raw', 'hash-2', ?, ?),
			(3, 8, 'jarvis', 'evt_2', 'Old memory', 'raw', 'hash-3', ?, ?)`,
		now, now, now, now, now, now); err != nil {
		t.Fatalf("insert records: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v3 database: %v", err)
	}

	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	version, err := st.SchemaVersion(context.Background())
	if err != nil || version != 4 {
		t.Fatalf("SchemaVersion() = %d, %v, want 4", version, err)
	}

	clientsList, err := st.ListClients(context.Background())
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	want := map[int64]struct{ kind, source string }{
		1: {"agent", "opencode-macbook"},
		2: {"agent", "jarvis"},
		3: {"ui", ""},
		4: {"agent", "opencode-macbook-4"},
		5: {"agent", "foo-1"},
		6: {"agent", "foo"},
		7: {"agent", "foo-7"},
		8: {"agent", "jarvis"},
		9: {"agent", "foo-7-9"},
	}
	if len(clientsList) != len(want) {
		t.Fatalf("clients = %d, want %d", len(clientsList), len(want))
	}
	for _, client := range clientsList {
		expected, ok := want[client.ID]
		if !ok {
			t.Fatalf("unexpected client %d", client.ID)
		}
		if string(client.Kind) != expected.kind || client.Source != expected.source {
			t.Errorf("client %d = (%q, %q), want %+v", client.ID, client.Kind, client.Source, expected)
		}
	}

	for id, wantSource := range map[int64]string{1: "opencode-macbook", 2: "jarvis", 3: "jarvis"} {
		var source string
		if err := st.db.QueryRow(`SELECT source FROM records WHERE id = ?`, id).Scan(&source); err != nil {
			t.Fatalf("read record %d source: %v", id, err)
		}
		if source != wantSource {
			t.Errorf("record %d source = %q, want %q", id, source, wantSource)
		}
	}

	var foreignKeys int
	if err := st.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, %v, want 1", foreignKeys, err)
	}
	fkRows, err := st.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if fkRows.Next() {
		fkRows.Close()
		t.Fatal("foreign_key_check reported a violation after the migration")
	}
	fkRows.Close()

	if _, err := st.db.Exec(`
		INSERT INTO clients (name, kind, source, token_hash, scopes, created_at)
		VALUES ('duplicate', 'agent', 'foo', 'hash-dup', 'ingest', ?)`, now); err == nil {
		t.Fatal("active duplicate source accepted, want a unique index violation")
	}
	if _, err := st.db.Exec(`UPDATE clients SET revoked_at = ? WHERE id = 2`, now); err != nil {
		t.Fatalf("revoke client: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO clients (name, kind, source, token_hash, scopes, created_at)
		VALUES ('jarvis-new', 'agent', 'jarvis', 'hash-new', 'ingest', ?)`, now); err != nil {
		t.Fatalf("reusing a revoked source failed: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO records (client_id, source, external_id, title, raw_text, content_hash, updated_at, indexed_at)
		VALUES (999, 'x', 'evt', 't', 'r', 'h', ?, ?)`, now, now); err == nil {
		t.Fatal("record with an unknown client accepted, want a foreign key violation")
	}
}

// The memory write path stores `indexed` idempotency rows, which the original
// CHECK constraint rejected. The migration rebuilds the table, so it must keep
// existing ingest rows intact.
func TestMemoryIdempotencyMigrationPreservesRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")
	db, err := sql.Open(driverName, "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	apply := func(name string) {
		t.Helper()
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	apply("0001_clients.sql")
	apply("0002_records.sql")

	now := "2026-11-04T12:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO clients (id, name, kind, token_hash, scopes, created_at)
		VALUES (1, 'jarvis-writer', 'jarvis', 'hash', 'ingest', ?)`, now); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	record, err := db.Exec(`
		INSERT INTO records (client_id, source, external_id, title, raw_text, content_hash, updated_at, indexed_at)
		VALUES (1, 'jarvis', 'evt_1', 'Memory', 'raw', 'content-hash', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert record: %v", err)
	}
	recordID, err := record.LastInsertId()
	if err != nil {
		t.Fatalf("record id: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO idempotency_keys (client_id, operation, key, request_hash, record_id, status, created_at)
		VALUES (1, 'ingest', 'key-1', 'request-hash', ?, 'queued', ?)`, recordID, now); err != nil {
		t.Fatalf("insert idempotency row: %v", err)
	}

	apply("0003_memory_idempotency.sql")

	var (
		status      string
		requestHash string
	)
	if err := db.QueryRow(`
		SELECT status, request_hash FROM idempotency_keys
		WHERE client_id = 1 AND operation = 'ingest' AND key = 'key-1'`,
	).Scan(&status, &requestHash); err != nil {
		t.Fatalf("existing idempotency row did not survive the rebuild: %v", err)
	}
	if status != "queued" || requestHash != "request-hash" {
		t.Errorf("existing row = (%q, %q), want (queued, request-hash)", status, requestHash)
	}

	if _, err := db.Exec(`
		INSERT INTO idempotency_keys (client_id, operation, key, request_hash, record_id, status, created_at)
		VALUES (1, 'memory', 'key-2', 'request-hash-2', ?, 'indexed', ?)`, recordID, now); err != nil {
		t.Fatalf("indexed status rejected after the migration: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO idempotency_keys (client_id, operation, key, request_hash, record_id, status, created_at)
		VALUES (1, 'memory', 'key-3', 'request-hash-3', ?, 'bogus', ?)`, recordID, now); err == nil {
		t.Fatal("unknown idempotency status accepted, want a CHECK violation")
	}
}
