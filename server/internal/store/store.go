// Package store owns the SQLite database: connection setup, migrations and
// the repositories used by the HTTP API.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound is returned when a requested row does not exist or is revoked.
	ErrNotFound = errors.New("not found")
	// ErrClientNameTaken is returned when an active client already uses the name.
	ErrClientNameTaken = errors.New("client name already taken")
	// ErrClientSourceTaken is returned when an active client already uses the source.
	ErrClientSourceTaken = errors.New("client source already taken")
	// ErrClientConflict is returned for an unexpected uniqueness conflict.
	ErrClientConflict = errors.New("client conflicts with an active client")
	// ErrInvalidClient is returned when client input violates the domain rules.
	ErrInvalidClient = errors.New("invalid client")
)

const (
	driverName  = "sqlite"
	busyTimeout = 5000
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store is the process-wide SQLite store. It keeps a single connection so the
// connection-scoped PRAGMAs and the single-writer guarantee are preserved.
type Store struct {
	db   *sql.DB
	fts5 bool
}

// Health is a point-in-time snapshot of the database state exposed at /health.
type Health struct {
	SchemaVersion int
	FTS5          bool
}

// Open opens (and creates when needed) the SQLite database, configures it,
// applies pending migrations and verifies that FTS5 is available.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is empty")
	}
	if path == ":memory:" {
		return nil, errors.New("in-memory database is not supported; use a file path")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	dsn, err := openDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	st := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := st.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := st.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := st.checkFTS5(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return st, nil
}

// openDSN sets connection-scoped PRAGMAs in the DSN so every connection the
// pool opens — including a replacement after a driver error — gets them.
func openDSN(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}

	query := url.Values{}
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout))
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")

	dsn := &url.URL{Scheme: "file", Path: absolute, RawQuery: query.Encode()}
	return dsn.String(), nil
}

func (s *Store) configure(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("verify WAL: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("verify WAL: SQLite reported journal_mode=%q", mode)
	}

	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return errors.New("verify foreign_keys: foreign key enforcement is off")
	}

	var timeout int
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
		return fmt.Errorf("verify busy_timeout: %w", err)
	}
	if timeout != busyTimeout {
		return fmt.Errorf("verify busy_timeout: got %d, want %d", timeout, busyTimeout)
	}

	var synchronous int
	if err := s.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return fmt.Errorf("verify synchronous: %w", err)
	}
	if synchronous != 1 {
		return fmt.Errorf("verify synchronous: got %d, want 1 (NORMAL)", synchronous)
	}

	return nil
}

func (s *Store) migrate(ctx context.Context) (err error) {
	// Rebuilding a table (for example clients, whose CHECK/unique indexes must
	// change) drops the old table while child tables still reference its ids.
	// Foreign keys are disabled for the migration run and verified back on
	// afterwards; every migration preserves ids.
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for migrations: %w", err)
	}
	defer func() {
		restoreCtx := context.WithoutCancel(ctx)
		if _, ferr := s.db.ExecContext(restoreCtx, "PRAGMA foreign_keys = ON"); ferr != nil {
			if err == nil {
				err = fmt.Errorf("re-enable foreign keys: %w", ferr)
			}
			return
		}
		var enabled int
		if qerr := s.db.QueryRowContext(restoreCtx, "PRAGMA foreign_keys").Scan(&enabled); qerr != nil {
			if err == nil {
				err = fmt.Errorf("verify foreign keys after migrations: %w", qerr)
			}
			return
		}
		if enabled != 1 && err == nil {
			err = errors.New("verify foreign keys after migrations: foreign key enforcement is off")
		}
	}()

	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := migrationVersion(filepath.Base(name))
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}

		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if version == 4 {
			if err := migrateGenericClients(ctx, tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			version, formatTime(time.Now()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	// A rebuild must not leave dangling references. This runs before foreign
	// keys are re-enabled in the deferred cleanup.
	fkRows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check foreign keys after migrations: %w", err)
	}
	defer fkRows.Close()
	if fkRows.Next() {
		return errors.New("migrations left foreign key violations")
	}
	if err := fkRows.Err(); err != nil {
		return fmt.Errorf("check foreign keys after migrations: %w", err)
	}

	return nil
}

func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must be named <version>_<name>.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has an invalid version", name)
	}
	return version, nil
}

func (s *Store) checkFTS5(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "CREATE VIRTUAL TABLE temp.fts5_probe USING fts5(value)"); err != nil {
		return fmt.Errorf("FTS5 is not available: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE temp.fts5_probe"); err != nil {
		return fmt.Errorf("drop FTS5 probe: %w", err)
	}
	s.fts5 = true
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// Health checks that the database can serve queries, that it is writable and
// reports its state.
func (s *Store) Health(ctx context.Context) (Health, error) {
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return Health{FTS5: s.fts5}, fmt.Errorf("database check: %w", err)
	}

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return Health{FTS5: s.fts5}, err
	}

	if err := s.checkWritable(ctx); err != nil {
		return Health{SchemaVersion: version, FTS5: s.fts5}, err
	}

	return Health{SchemaVersion: version, FTS5: s.fts5}, nil
}

// checkWritable proves that the database accepts writes without leaving a
// probe table behind: the DDL is rolled back either way.
func (s *Store) checkWritable(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write probe: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE health_write_probe (id INTEGER)"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("write probe: %w", err)
	}
	if err := tx.Rollback(); err != nil {
		return fmt.Errorf("rollback write probe: %w", err)
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
