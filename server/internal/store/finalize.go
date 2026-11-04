package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// FinalizeIdleSessions marks unfinished sessions idle for at least idle as
// final and enqueues one card job for each. The operation is one transaction,
// so a crash cannot finalize a session without queueing its card.
func (s *Store) FinalizeIdleSessions(ctx context.Context, now time.Time, idle time.Duration) (int, error) {
	if idle <= 0 {
		return 0, errors.New("idle must be positive")
	}
	now = now.UTC()
	cutoff := now.Add(-idle)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, client_id, source, external_id
		FROM records
		WHERE is_final = 0 AND kind = 'session'
		  AND julianday(updated_at) < julianday(?)
		ORDER BY id`,
		formatTime(cutoff),
	)
	if err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}

	type idleSession struct {
		id         int64
		clientID   int64
		source     string
		externalID string
	}
	var sessions []idleSession
	for rows.Next() {
		var session idleSession
		if err := rows.Scan(&session.id, &session.clientID, &session.source, &session.externalID); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return 0, fmt.Errorf("finalize idle sessions: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback()
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}

	finalized := 0
	for _, session := range sessions {
		result, err := tx.ExecContext(ctx, `
			UPDATE records
			SET is_final = 1, finalized_at = ?, card_status = ?,
				revision = revision + 1
			WHERE id = ? AND is_final = 0`,
			formatTime(now), CardStatusQueued, session.id,
		)
		if err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("finalize record %d: %w", session.id, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("finalize record %d: %w", session.id, err)
		}
		if affected == 0 {
			continue
		}
		if _, err := insertIngestJob(ctx, tx, session.clientID, "", session.source, session.externalID, session.id, now); err != nil {
			_ = tx.Rollback()
			return 0, fmt.Errorf("enqueue finalized record %d: %w", session.id, err)
		}
		finalized++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}
	return finalized, nil
}
