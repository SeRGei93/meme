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

const clientColumns = "id, name, kind, scopes, created_at, last_seen_at, revoked_at"

// CreateClient stores a new client and returns its plaintext token exactly once.
func (s *Store) CreateClient(ctx context.Context, name string, kind auth.Kind, scopes []auth.Scope) (auth.Client, string, error) {
	if err := auth.ValidateClient(name, kind, scopes); err != nil {
		return auth.Client{}, "", fmt.Errorf("%w: %w", ErrInvalidClient, err)
	}

	token, hash, err := auth.GenerateToken(kind)
	if err != nil {
		return auth.Client{}, "", err
	}

	createdAt := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO clients (name, kind, token_hash, scopes, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) WHERE revoked_at IS NULL DO NOTHING`,
		name, string(kind), hash, encodeScopes(scopes), formatTime(createdAt),
	)
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}
	if rows == 0 {
		return auth.Client{}, "", ErrClientNameTaken
	}

	id, err := result.LastInsertId()
	if err != nil {
		return auth.Client{}, "", fmt.Errorf("create client: %w", err)
	}

	return auth.Client{
		ID:        id,
		Name:      name,
		Kind:      kind,
		Scopes:    append([]auth.Scope(nil), scopes...),
		CreatedAt: createdAt,
	}, token, nil
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
		scopes     string
		createdAt  string
		lastSeenAt sql.NullString
		revokedAt  sql.NullString
	)
	if err := scanner.Scan(
		&client.ID, &client.Name, &kind, &scopes, &createdAt, &lastSeenAt, &revokedAt,
	); err != nil {
		return auth.Client{}, err
	}

	client.Kind = auth.Kind(kind)
	if !client.Kind.Valid() {
		return auth.Client{}, fmt.Errorf("client %d: stored kind %q is invalid", client.ID, kind)
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
