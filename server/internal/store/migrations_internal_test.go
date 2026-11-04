package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
