package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/mask"
)

// ErrInvalidJobQuery is returned when a job listing or lifecycle call violates
// the queue contract.
var ErrInvalidJobQuery = errors.New("invalid job query")

// JobStatus is the lifecycle state stored on ingest_jobs.
type JobStatus string

const (
	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobDone    JobStatus = "done"
	JobFailed  JobStatus = "failed"
)

// Valid reports whether the status is part of the v1 contract.
func (s JobStatus) Valid() bool {
	switch s {
	case JobQueued, JobRunning, JobDone, JobFailed:
		return true
	default:
		return false
	}
}

const (
	defaultJobLimit  = 50
	maxJobLimit      = 200
	maxJobErrorRunes = 2000
)

const jobColumns = `id, client_id, kind, status, attempts, external_id, record_id,
	next_retry_at, error, created_at, updated_at`

// Job is one row from the ingestion queue.
type Job struct {
	ID          int64
	ClientID    int64
	Kind        string
	Status      JobStatus
	Attempts    int
	ExternalID  string
	RecordID    *int64
	NextRetryAt *time.Time
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ListJobsParams filters a newest-first page of jobs. Limit defaults to 50.
// BeforeID paginates: only jobs with a smaller id are returned.
type ListJobsParams struct {
	Status   JobStatus
	Limit    int
	BeforeID int64
}

// JobPage is one page of jobs and whether another page exists.
type JobPage struct {
	Items   []Job
	HasMore bool
}

// GetJob returns one job by id.
func (s *Store) GetJob(ctx context.Context, id int64) (Job, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM ingest_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job %d: %w", id, err)
	}
	return job, nil
}

// ClaimNextJob atomically moves the oldest due queued job to running and
// increments its attempt counter. It returns false when nothing is due.
func (s *Store) ClaimNextJob(ctx context.Context, now time.Time) (Job, bool, error) {
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, fmt.Errorf("claim job: %w", err)
	}

	job, err := scanJob(tx.QueryRowContext(ctx, `
		SELECT `+jobColumns+`
		FROM ingest_jobs
		WHERE status = ?
		  AND (next_retry_at IS NULL OR julianday(next_retry_at) <= julianday(?))
		ORDER BY id
		LIMIT 1`,
		string(JobQueued), formatTime(now),
	))
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return Job{}, false, nil
	}
	if err != nil {
		_ = tx.Rollback()
		return Job{}, false, fmt.Errorf("claim job: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE ingest_jobs
		SET status = ?, attempts = attempts + 1, next_retry_at = NULL, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(JobRunning), formatTime(now), job.ID, string(JobQueued),
	)
	if err != nil {
		_ = tx.Rollback()
		return Job{}, false, fmt.Errorf("claim job %d: %w", job.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return Job{}, false, fmt.Errorf("claim job %d: %w", job.ID, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return Job{}, false, nil
	}

	if err := tx.Commit(); err != nil {
		return Job{}, false, fmt.Errorf("claim job %d: %w", job.ID, err)
	}

	job.Status = JobRunning
	job.Attempts++
	job.NextRetryAt = nil
	job.UpdatedAt = now
	return job, true, nil
}

// CompleteJob marks a running job done and clears its retry state.
func (s *Store) CompleteJob(ctx context.Context, id int64, now time.Time) error {
	return s.finishJob(ctx, id, JobDone, now, nil, "")
}

// RetryJob returns a running job to the queue with the next retry instant.
func (s *Store) RetryJob(ctx context.Context, id int64, now, next time.Time, errText string) error {
	return s.finishJob(ctx, id, JobQueued, now, &next, errText)
}

// FailJob marks a running job failed after its final attempt.
func (s *Store) FailJob(ctx context.Context, id int64, now time.Time, errText string) error {
	return s.finishJob(ctx, id, JobFailed, now, nil, errText)
}

func (s *Store) finishJob(ctx context.Context, id int64, status JobStatus, at time.Time, next *time.Time, errText string) error {
	var nextValue any
	if next != nil {
		nextValue = formatTime(next.UTC())
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE ingest_jobs
		SET status = ?, next_retry_at = ?, error = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(status), nextValue, sanitizeJobError(errText), formatTime(at),
		id, string(JobRunning),
	)
	if err != nil {
		return fmt.Errorf("finish job %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finish job %d: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("finish job %d: job is not running", id)
	}
	return nil
}

// RecoverRunningJobs moves jobs left in running by a previous process back to
// the queue. The interrupted attempt stays counted.
func (s *Store) RecoverRunningJobs(ctx context.Context, now time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE ingest_jobs
		SET status = ?, next_retry_at = NULL, updated_at = ?
		WHERE status = ?`,
		string(JobQueued), formatTime(now), string(JobRunning),
	)
	if err != nil {
		return 0, fmt.Errorf("recover running jobs: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("recover running jobs: %w", err)
	}
	return int(affected), nil
}

// ListJobs returns one newest-first page of jobs.
func (s *Store) ListJobs(ctx context.Context, params ListJobsParams) (JobPage, error) {
	limit := params.Limit
	if limit == 0 {
		limit = defaultJobLimit
	}
	if limit < 1 || limit > maxJobLimit {
		return JobPage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidJobQuery, maxJobLimit)
	}
	if params.Status != "" && !params.Status.Valid() {
		return JobPage{}, fmt.Errorf("%w: unknown status %q", ErrInvalidJobQuery, params.Status)
	}
	if params.BeforeID < 0 {
		return JobPage{}, fmt.Errorf("%w: cursor must be a positive job id", ErrInvalidJobQuery)
	}

	var (
		clauses []string
		args    []any
	)
	if params.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(params.Status))
	}
	if params.BeforeID > 0 {
		clauses = append(clauses, "id < ?")
		args = append(args, params.BeforeID)
	}

	query := `SELECT ` + jobColumns + ` FROM ingest_jobs`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return JobPage{}, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	page := JobPage{Items: make([]Job, 0, limit)}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return JobPage{}, fmt.Errorf("list jobs: %w", err)
		}
		page.Items = append(page.Items, job)
	}
	if err := rows.Err(); err != nil {
		return JobPage{}, fmt.Errorf("list jobs: %w", err)
	}

	if len(page.Items) > limit {
		page.HasMore = true
		page.Items = page.Items[:limit]
	}
	return page, nil
}

// FinalizeIdleSessions marks unfinished sessions idle for at least idle as
// final and enqueues one card job for each. The operation is one transaction,
// so a crash cannot finalize a session without queueing its card.
func (s *Store) FinalizeIdleSessions(ctx context.Context, now time.Time, idle time.Duration) (int, error) {
	if idle <= 0 {
		return 0, fmt.Errorf("%w: idle must be positive", ErrInvalidJobQuery)
	}
	now = now.UTC()
	cutoff := now.Add(-idle)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("finalize idle sessions: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id, client_id, source, external_id, revision
		FROM records
		WHERE is_final = 0 AND julianday(updated_at) < julianday(?)
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
		revision   int64
	}
	var sessions []idleSession
	for rows.Next() {
		var session idleSession
		if err := rows.Scan(&session.id, &session.clientID, &session.source, &session.externalID, &session.revision); err != nil {
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
			SET is_final = 1, finalized_at = ?, card_status = 'queued'
			WHERE id = ? AND is_final = 0`,
			formatTime(now), session.id,
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
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO ingest_jobs
				(client_id, idempotency_key, source, external_id, record_id, kind, status, created_at, updated_at)
			VALUES (?, '', ?, ?, ?, 'ingest', 'queued', ?, ?)`,
			session.clientID, session.source, session.externalID, session.id,
			formatTime(now), formatTime(now),
		); err != nil {
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

func scanJob(scanner rowScanner) (Job, error) {
	var (
		job         Job
		status      string
		recordID    sql.NullInt64
		nextRetryAt sql.NullString
		createdAt   string
		updatedAt   string
	)
	if err := scanner.Scan(
		&job.ID, &job.ClientID, &job.Kind, &status, &job.Attempts, &job.ExternalID,
		&recordID, &nextRetryAt, &job.Error, &createdAt, &updatedAt,
	); err != nil {
		return Job{}, err
	}
	job.Status = JobStatus(status)
	if recordID.Valid {
		id := recordID.Int64
		job.RecordID = &id
	}
	var err error
	if job.NextRetryAt, err = parseNullableTime(nextRetryAt); err != nil {
		return Job{}, err
	}
	if job.CreatedAt, err = parseTime(createdAt); err != nil {
		return Job{}, err
	}
	if job.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Job{}, err
	}
	return job, nil
}

// sanitizeJobError masks credentials before an error is persisted or exposed
// through GET /api/v1/jobs.
func sanitizeJobError(errText string) string {
	if errText == "" {
		return ""
	}
	masked := mask.Text(errText)
	runes := []rune(masked)
	if len(runes) > maxJobErrorRunes {
		masked = string(runes[:maxJobErrorRunes]) + "..."
	}
	return masked
}
