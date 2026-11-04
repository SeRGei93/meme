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

// ErrInvalidJobQuery is returned when a job listing violates the queue
// contract.
var ErrInvalidJobQuery = errors.New("invalid job query")

// ErrJobNotRunning is returned when a lifecycle update targets a job that is
// no longer in the running state.
var ErrJobNotRunning = errors.New("job is not running")

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
	// DefaultJobLimit is the contract's page size when limit is omitted.
	DefaultJobLimit = 50
	// MaxJobLimit is the contract's upper bound for GET /api/v1/jobs?limit.
	MaxJobLimit = 200

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
	return s.finishAttempt(ctx, id, JobDone, now, nil, "")
}

// RetryJob returns a running job to the queue with the next retry instant.
func (s *Store) RetryJob(ctx context.Context, id int64, now, next time.Time, errText string) error {
	return s.finishAttempt(ctx, id, JobQueued, now, &next, errText)
}

// FailJob marks a running job failed after its final attempt and moves the
// associated card to the failed state.
func (s *Store) FailJob(ctx context.Context, id int64, now time.Time, errText string) error {
	return s.finishAttempt(ctx, id, JobFailed, now, nil, errText)
}

func (s *Store) finishAttempt(ctx context.Context, id int64, status JobStatus, at time.Time, next *time.Time, errText string) error {
	var nextValue any
	if next != nil {
		nextValue = formatTime(next.UTC())
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("finish job %d: %w", id, err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE ingest_jobs
		SET status = ?, next_retry_at = ?, error = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		string(status), nextValue, sanitizeJobError(errText), formatTime(at),
		id, string(JobRunning),
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("finish job %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("finish job %d: %w", id, err)
	}
	if affected == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("%w: job %d", ErrJobNotRunning, id)
	}

	if status == JobFailed {
		// Only fail the card when the job still belongs to the record's current
		// final version. A leftover job for an older version must not mark the
		// newer version's queued card as failed. Memories share the same
		// invariant: finalized_at and the job's created_at are stamped together.
		if _, err := tx.ExecContext(ctx, `
			UPDATE records
			SET card_status = ?
			WHERE card_status = ?
			  AND id = (SELECT record_id FROM ingest_jobs WHERE id = ? AND kind IN ('ingest', 'memory'))
			  AND COALESCE(finalized_at, '') = (
				SELECT created_at FROM ingest_jobs WHERE id = ? AND kind IN ('ingest', 'memory')
			  )`,
			CardStatusFailed, CardStatusQueued, id, id,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("fail card for job %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish job %d: %w", id, err)
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
		limit = DefaultJobLimit
	}
	if limit < 1 || limit > MaxJobLimit {
		return JobPage{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidJobQuery, MaxJobLimit)
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
