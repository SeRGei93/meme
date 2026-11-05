package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
)

const clientColumns = "id, name, kind, source, scopes, created_at, last_seen_at, revoked_at"

// CreateClient stores a new client and returns its plaintext token exactly once.
// An empty source defaults to a slug of the client name for agent clients.
// ui and admin clients never have a source.
func (s *Store) CreateClient(ctx context.Context, name string, kind auth.Kind, source string, scopes []auth.Scope) (auth.Client, string, error) {
	kind = auth.NormalizeKind(kind)
	if kind.AllowsSource() && source == "" {
		source = auth.DeriveSource(name)
	}
	if err := auth.ValidateClient(name, kind, source, scopes); err != nil {
		return auth.Client{}, "", fmt.Errorf("%w: %w", ErrInvalidClient, err)
	}

	token, hash, err := auth.GenerateToken(kind)
	if err != nil {
		return auth.Client{}, "", err
	}

	var storedSource any
	if source != "" {
		storedSource = source
	}

	createdAt := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO clients (name, kind, source, token_hash, scopes, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		name, string(kind), storedSource, hash, encodeScopes(scopes), formatTime(createdAt),
	)
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}
	if rows == 0 {
		return auth.Client{}, "", s.clientConflict(ctx, name, source)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}

	return auth.Client{
		ID:        id,
		Name:      name,
		Kind:      kind,
		Source:    source,
		Scopes:    append([]auth.Scope(nil), scopes...),
		CreatedAt: createdAt,
	}, token, nil
}

// clientConflict reports which active uniqueness rule rejected the insert.
func (s *Store) clientConflict(ctx context.Context, name, source string) error {
	var nameTaken, sourceTaken bool
	err := s.db.QueryRowContext(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM clients WHERE revoked_at IS NULL AND name = ?),
			EXISTS(SELECT 1 FROM clients WHERE revoked_at IS NULL AND source IS NOT NULL AND source = ?)`,
		name, source,
	).Scan(&nameTaken, &sourceTaken)
	if err != nil {
		return fmt.Errorf("resolve client conflict: %w", err)
	}
	switch {
	case nameTaken:
		return ErrClientNameTaken
	case sourceTaken:
		return ErrClientSourceTaken
	default:
		return ErrClientConflict
	}
}

// FindClientByTokenHash resolves an active client by the hash of its bearer token.
func (s *Store) FindClientByTokenHash(ctx context.Context, hash string) (auth.Client, error) {
	client, err := scanClient(s.db.QueryRowContext(ctx, `
		SELECT `+clientColumns+`
		FROM clients
		WHERE token_hash = ? AND revoked_at IS NULL`,
		hash,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Client{}, ErrNotFound
	}
	if err != nil {
		return auth.Client{}, fmt.Errorf("find client: %w", err)
	}
	return client, nil
}

// ListClients returns every client, including revoked ones, in creation order.
func (s *Store) ListClients(ctx context.Context) ([]auth.Client, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+clientColumns+`
		FROM clients
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()

	clients := make([]auth.Client, 0)
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, fmt.Errorf("list clients: %w", err)
		}
		clients = append(clients, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	return clients, nil
}

// RevokeClient revokes a client immediately. Revoking an already revoked
// client is a no-op; an unknown id returns ErrNotFound.
func (s *Store) RevokeClient(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE clients
		SET revoked_at = COALESCE(revoked_at, ?)
		WHERE id = ?`,
		formatTime(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("revoke client: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke client: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanClient(scanner rowScanner) (auth.Client, error) {
	var (
		client     auth.Client
		kind       string
		source     sql.NullString
		scopes     string
		createdAt  string
		lastSeenAt sql.NullString
		revokedAt  sql.NullString
	)
	if err := scanner.Scan(
		&client.ID, &client.Name, &kind, &source, &scopes, &createdAt, &lastSeenAt, &revokedAt,
	); err != nil {
		return auth.Client{}, err
	}

	client.Kind = auth.NormalizeKind(auth.Kind(kind))
	if !client.Kind.Stored() {
		return auth.Client{}, fmt.Errorf("client %d: stored kind %q is invalid", client.ID, kind)
	}
	if source.Valid {
		client.Source = source.String
	}
	var err error
	client.Scopes, err = decodeScopes(scopes)
	if err != nil {
		return auth.Client{}, fmt.Errorf("client %d: %w", client.ID, err)
	}
	client.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return auth.Client{}, fmt.Errorf("client %d: %w", client.ID, err)
	}
	client.LastSeenAt, err = parseNullableTime(lastSeenAt)
	if err != nil {
		return auth.Client{}, fmt.Errorf("client %d: %w", client.ID, err)
	}
	client.RevokedAt, err = parseNullableTime(revokedAt)
	if err != nil {
		return auth.Client{}, fmt.Errorf("client %d: %w", client.ID, err)
	}

	return client, nil
}

func encodeScopes(scopes []auth.Scope) string {
	return strings.Join(auth.ScopeStrings(scopes), ",")
}

func decodeScopes(value string) ([]auth.Scope, error) {
	if value == "" {
		return nil, errors.New("stored scopes are empty")
	}
	parts := strings.Split(value, ",")
	scopes := make([]auth.Scope, len(parts))
	for i, part := range parts {
		scope := auth.Scope(part)
		if !scope.Valid() {
			return nil, fmt.Errorf("stored scope %q is invalid", part)
		}
		scopes[i] = scope
	}
	return scopes, nil
}

// migrateGenericClients finishes migration 0004. It turns legacy source labels
// into the same slug as new clients, keeps one unique source per active client
// (revoked labels may be reused for rotation), rewrites historical records and
// installs the partial unique index. SQL cannot express the collision loop
// safely, so it runs in Go inside the migration transaction.
func migrateGenericClients(ctx context.Context, tx *sql.Tx) error {
	type legacyClient struct {
		id     int64
		name   string
		active bool
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, revoked_at IS NULL, source IS NOT NULL
		FROM clients
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list legacy clients: %w", err)
	}
	var clients []legacyClient
	for rows.Next() {
		var (
			client    legacyClient
			hasSource bool
		)
		if err := rows.Scan(&client.id, &client.name, &client.active, &hasSource); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy client: %w", err)
		}
		if hasSource {
			clients = append(clients, client)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list legacy clients: %w", err)
	}
	rows.Close()

	// Temporary unique labels let the final values be assigned in any order
	// without tripping the unique index transiently.
	for _, client := range clients {
		if _, err := tx.ExecContext(ctx,
			`UPDATE clients SET source = ? WHERE id = ?`,
			fmt.Sprintf("source-%d", client.id), client.id,
		); err != nil {
			return fmt.Errorf("normalize client %d source: %w", client.id, err)
		}
	}

	used := make(map[string]bool, len(clients))
	for _, client := range clients {
		source := auth.DeriveSource(client.name)
		if client.active {
			base := source
			for attempt := 1; used[source]; attempt++ {
				source = sourceWithSuffix(base, client.id, attempt)
			}
			used[source] = true
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE clients SET source = ? WHERE id = ?`, source, client.id,
		); err != nil {
			return fmt.Errorf("set client %d source: %w", client.id, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE records
		SET source = (SELECT c.source FROM clients c WHERE c.id = records.client_id)
		WHERE EXISTS (
			SELECT 1 FROM clients c WHERE c.id = records.client_id AND c.source IS NOT NULL
		)`); err != nil {
		return fmt.Errorf("backfill record sources: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE UNIQUE INDEX clients_active_source
		ON clients(source)
		WHERE revoked_at IS NULL AND source IS NOT NULL`); err != nil {
		return fmt.Errorf("create active source index: %w", err)
	}
	return nil
}

func sourceWithSuffix(base string, id int64, attempt int) string {
	suffix := fmt.Sprintf("-%d", id)
	if attempt > 1 {
		suffix = fmt.Sprintf("-%d-%d", id, attempt)
	}
	limit := auth.MaxSourceLen - len(suffix)
	if limit < 1 {
		limit = 1
	}
	if len(base) > limit {
		base = strings.TrimRight(base[:limit], "-")
	}
	if base == "" {
		base = auth.DefaultSource
	}
	return base + suffix
}
