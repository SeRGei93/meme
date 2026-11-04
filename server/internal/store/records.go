package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/ingest"
)

// ErrIdempotencyKeyReused is returned when an Idempotency-Key is sent again
// with a different request body.
var ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different body")

const idempotencyTTL = 24 * time.Hour

// Card statuses from the v1 contract. A heuristic card is written on ingest;
// the queue advances it to ok or failed.
const (
	CardStatusHeuristic = "heuristic"
	CardStatusQueued    = "queued"
	CardStatusOK        = "ok"
	CardStatusFailed    = "failed"
)

// IngestParams is one validated session bundle ready to be persisted.
type IngestParams struct {
	ClientID       int64
	Source         string
	IdempotencyKey string
	Bundle         ingest.Bundle
	Now            time.Time
}

// preparedIngest derives everything the store needs from the bundle once, so
// the content hash, raw text and heuristic card cannot drift apart.
type preparedIngest struct {
	IngestParams
	requestHash string
	contentHash string
	rawText     string
	card        ingest.Card
}

func prepareIngest(params IngestParams) preparedIngest {
	// The idempotency hash must cover the exact request body so a retry that
	// changes even a secret value is still treated as a different request.
	requestHash := params.Bundle.RequestHash()

	// Mask before rendering raw text or building the card: secrets must never
	// reach SQLite or a later LLM prompt. The content hash is computed after
	// masking, so captures that differ only in secret values have the same
	// stored knowledge and do not create a new revision.
	params.Bundle = params.Bundle.Masked()
	return preparedIngest{
		IngestParams: params,
		requestHash:  requestHash,
		contentHash:  params.Bundle.ContentHash(),
		rawText:      params.Bundle.RawText(),
		card:         params.Bundle.HeuristicCard(),
	}
}

// IngestResult is the response returned by POST /api/v1/ingest.
type IngestResult struct {
	RecordID int64
	JobID    *int64
	Status   string
	Replayed bool
}

// Record is a stored knowledge record joined with its project key.
type Record struct {
	ID          int64
	ClientID    int64
	Source      string
	ExternalID  string
	ProjectKey  string
	Kind        string
	Title       string
	Summary     string
	Questions   []string
	Keywords    []string
	Atoms       []string
	RawText     string
	ContentHash string
	Revision    int64
	IsFinal     bool
	FinalizedAt *time.Time
	StartedAt   *time.Time
	UpdatedAt   time.Time
	IndexedAt   time.Time
	CardModel   *string
	CardStatus  string
	Metadata    map[string]any
}

// IngestSession validates idempotency, creates or updates the session record,
// finds or creates its project and queues card generation when the bundle is
// final. The whole operation is one SQLite transaction.
func (s *Store) IngestSession(ctx context.Context, params IngestParams) (IngestResult, error) {
	if params.Now.IsZero() {
		params.Now = time.Now().UTC()
	} else {
		params.Now = params.Now.UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IngestResult{}, fmt.Errorf("begin ingest: %w", err)
	}

	result, err := ingestSessionTx(ctx, tx, prepareIngest(params))
	if err != nil {
		_ = tx.Rollback()
		return IngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return IngestResult{}, fmt.Errorf("commit ingest: %w", err)
	}
	return result, nil
}

func ingestSessionTx(ctx context.Context, tx *sql.Tx, params preparedIngest) (IngestResult, error) {
	if replayed, ok, err := checkIdempotency(ctx, tx, params); err != nil {
		return IngestResult{}, err
	} else if ok {
		return replayed, nil
	}

	projectID, err := upsertProject(ctx, tx, params.Bundle.ProjectKey, params.Bundle.ProjectPath, params.Now)
	if err != nil {
		return IngestResult{}, err
	}

	result, err := upsertSessionRecord(ctx, tx, params, projectID)
	if err != nil {
		return IngestResult{}, err
	}
	if err := saveIdempotency(ctx, tx, params, result); err != nil {
		return IngestResult{}, err
	}
	return result, nil
}

func checkIdempotency(ctx context.Context, tx *sql.Tx, params preparedIngest) (IngestResult, bool, error) {
	var (
		requestHash string
		recordID    int64
		jobID       sql.NullInt64
		status      string
		createdAt   string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT request_hash, record_id, job_id, status, created_at
		FROM idempotency_keys
		WHERE client_id = ? AND operation = 'ingest' AND key = ?`,
		params.ClientID, params.IdempotencyKey,
	).Scan(&requestHash, &recordID, &jobID, &status, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return IngestResult{}, false, nil
	}
	if err != nil {
		return IngestResult{}, false, fmt.Errorf("read idempotency key: %w", err)
	}

	created, err := parseTime(createdAt)
	if err != nil {
		return IngestResult{}, false, fmt.Errorf("read idempotency key: %w", err)
	}
	if params.Now.Sub(created) > idempotencyTTL {
		return IngestResult{}, false, nil
	}
	if requestHash != params.requestHash {
		return IngestResult{}, false, ErrIdempotencyKeyReused
	}

	return IngestResult{
		RecordID: recordID,
		JobID:    nullableInt64(jobID),
		Status:   status,
		Replayed: true,
	}, true, nil
}

func saveIdempotency(ctx context.Context, tx *sql.Tx, params preparedIngest, result IngestResult) error {
	var jobID any
	if result.JobID != nil {
		jobID = *result.JobID
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO idempotency_keys
			(client_id, operation, key, request_hash, record_id, job_id, status, created_at)
		VALUES (?, 'ingest', ?, ?, ?, ?, ?, ?)
		ON CONFLICT(client_id, operation, key) DO UPDATE SET
			request_hash = excluded.request_hash,
			record_id    = excluded.record_id,
			job_id       = excluded.job_id,
			status       = excluded.status,
			created_at   = excluded.created_at`,
		params.ClientID, params.IdempotencyKey, params.requestHash,
		result.RecordID, jobID, result.Status, formatTime(params.Now),
	)
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

func upsertProject(ctx context.Context, tx *sql.Tx, key, path string, now time.Time) (int64, error) {
	name := projectName(key)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO projects (key, name, path, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			path = CASE WHEN projects.path = '' THEN excluded.path ELSE projects.path END`,
		key, name, path, formatTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("upsert project: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE key = ?", key).Scan(&id); err != nil {
		return 0, fmt.Errorf("read project: %w", err)
	}
	return id, nil
}

func projectName(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '/' || r == ':'
	})
	if len(parts) == 0 {
		return key
	}
	return parts[len(parts)-1]
}

func upsertSessionRecord(ctx context.Context, tx *sql.Tx, params preparedIngest, projectID int64) (IngestResult, error) {
	var (
		existingID        int64
		existingHash      string
		existingUpdatedAt string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT id, content_hash, updated_at
		FROM records
		WHERE client_id = ? AND source = ? AND external_id = ?`,
		params.ClientID, params.Source, params.Bundle.ExternalID,
	).Scan(&existingID, &existingHash, &existingUpdatedAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return insertSessionRecord(ctx, tx, params, projectID)
	case err != nil:
		return IngestResult{}, fmt.Errorf("find session record: %w", err)
	}

	if existingHash != params.contentHash {
		updatedAt, err := parseTime(existingUpdatedAt)
		if err != nil {
			return IngestResult{}, fmt.Errorf("find session record: %w", err)
		}
		if params.Bundle.UpdatedAt.Before(updatedAt) {
			return replaySessionRecord(ctx, tx, existingID)
		}
		return updateSessionRecord(ctx, tx, params, projectID, existingID)
	}

	return replaySessionRecord(ctx, tx, existingID)
}

func replaySessionRecord(ctx context.Context, tx *sql.Tx, recordID int64) (IngestResult, error) {
	jobID, err := latestIngestJob(ctx, tx, recordID)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{
		RecordID: recordID,
		JobID:    jobID,
		Status:   statusForJob(jobID),
		Replayed: true,
	}, nil
}

func insertSessionRecord(ctx context.Context, tx *sql.Tx, params preparedIngest, projectID int64) (IngestResult, error) {
	cardStatus := CardStatusHeuristic
	var finalizedAt any
	if params.Bundle.Final {
		cardStatus = CardStatusQueued
		finalizedAt = formatTime(params.Now)
	}

	metadata, err := marshalMetadata(params.Bundle.Metadata)
	if err != nil {
		return IngestResult{}, err
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO records (
			client_id, source, external_id, project_id, kind, title, summary,
			questions, keywords, atoms, raw_text, content_hash, revision,
			is_final, finalized_at, started_at, updated_at, indexed_at,
			card_model, card_status, metadata_json
		) VALUES (?, ?, ?, ?, 'session', ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		params.ClientID, params.Source, params.Bundle.ExternalID, projectID,
		params.card.Title, params.card.Summary,
		joinList(params.card.Questions), joinList(params.card.Keywords), joinList(params.card.Atoms),
		params.rawText, params.contentHash, params.Bundle.Final, finalizedAt,
		formatTime(params.Bundle.StartedAt), formatTime(params.Bundle.UpdatedAt), formatTime(params.Now),
		cardStatus, metadata,
	)
	if err != nil {
		return IngestResult{}, fmt.Errorf("insert session record: %w", err)
	}

	recordID, err := result.LastInsertId()
	if err != nil {
		return IngestResult{}, fmt.Errorf("insert session record: %w", err)
	}

	jobID, err := enqueueIngestJob(ctx, tx, params, recordID)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{
		RecordID: recordID,
		JobID:    jobID,
		Status:   statusForJob(jobID),
		Replayed: false,
	}, nil
}

func updateSessionRecord(ctx context.Context, tx *sql.Tx, params preparedIngest, projectID, recordID int64) (IngestResult, error) {
	metadata, err := marshalMetadata(params.Bundle.Metadata)
	if err != nil {
		return IngestResult{}, err
	}

	if !params.Bundle.Final {
		// A live capture updates raw text only: it must not unfinalize the
		// session or destroy a card that is queued or already generated.
		_, err = tx.ExecContext(ctx, `
			UPDATE records SET
				project_id    = ?,
				raw_text      = ?,
				content_hash  = ?,
				revision      = revision + 1,
				updated_at    = ?,
				indexed_at    = ?,
				metadata_json = ?
			WHERE id = ?`,
			projectID, params.rawText, params.contentHash,
			formatTime(params.Bundle.UpdatedAt), formatTime(params.Now),
			metadata, recordID,
		)
		if err != nil {
			return IngestResult{}, fmt.Errorf("update live session record: %w", err)
		}
		return IngestResult{
			RecordID: recordID,
			JobID:    nil,
			Status:   "stored",
			Replayed: false,
		}, nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE records SET
			project_id    = ?,
			title         = ?,
			summary       = ?,
			questions     = ?,
			keywords      = ?,
			atoms         = ?,
			raw_text      = ?,
			content_hash  = ?,
			revision      = revision + 1,
			is_final      = 1,
			finalized_at  = ?,
			started_at    = ?,
			updated_at    = ?,
			indexed_at    = ?,
			card_model    = NULL,
			card_status   = ?,
			metadata_json = ?
		WHERE id = ?`,
		projectID, params.card.Title, params.card.Summary,
		joinList(params.card.Questions), joinList(params.card.Keywords), joinList(params.card.Atoms),
		params.rawText, params.contentHash, formatTime(params.Now),
		formatTime(params.Bundle.StartedAt), formatTime(params.Bundle.UpdatedAt), formatTime(params.Now),
		CardStatusQueued, metadata, recordID,
	)
	if err != nil {
		return IngestResult{}, fmt.Errorf("update session record: %w", err)
	}

	jobID, err := enqueueIngestJob(ctx, tx, params, recordID)
	if err != nil {
		return IngestResult{}, err
	}
	return IngestResult{
		RecordID: recordID,
		JobID:    jobID,
		Status:   statusForJob(jobID),
		Replayed: false,
	}, nil
}

func enqueueIngestJob(ctx context.Context, tx *sql.Tx, params preparedIngest, recordID int64) (*int64, error) {
	if !params.Bundle.Final {
		return nil, nil
	}

	id, err := insertIngestJob(ctx, tx, params.ClientID, params.IdempotencyKey, params.Source, params.Bundle.ExternalID, recordID, params.Now)
	if err != nil {
		return nil, fmt.Errorf("enqueue ingest job: %w", err)
	}
	return &id, nil
}

// insertIngestJob is the single insert path shared by live ingest and the
// idle-session finalizer.
func insertIngestJob(ctx context.Context, tx *sql.Tx, clientID int64, idempotencyKey, source, externalID string, recordID int64, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_jobs
			(client_id, idempotency_key, source, external_id, record_id, kind, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'ingest', 'queued', ?, ?)`,
		clientID, idempotencyKey, source, externalID, recordID,
		formatTime(now), formatTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("insert ingest job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert ingest job: %w", err)
	}
	return id, nil
}

func latestIngestJob(ctx context.Context, tx *sql.Tx, recordID int64) (*int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM ingest_jobs
		WHERE record_id = ?
		ORDER BY id DESC
		LIMIT 1`, recordID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find latest ingest job: %w", err)
	}
	return &id, nil
}

func statusForJob(jobID *int64) string {
	if jobID == nil {
		return "stored"
	}
	return "queued"
}

func nullableInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func marshalMetadata(metadata ingest.Metadata) (string, error) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(raw), nil
}

func joinList(values []string) string {
	return strings.Join(values, "\n")
}

func splitList(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}

// SaveCard replaces the heuristic card with an LLM-generated one and marks it
// ready. The FTS triggers index the new card in the same transaction. A changed
// finalized_at means a newer final version exists and already has its own job,
// so this card is discarded; live captures do not change finalized_at and keep
// the running card valid.
func (s *Store) SaveCard(ctx context.Context, recordID int64, finalizedAt *time.Time, card ingest.Card, model string, now time.Time) (bool, error) {
	var cardModel any
	if model != "" {
		cardModel = model
	}

	return s.updateQueuedCard(ctx, recordID, finalizedAt,
		"title = ?, summary = ?, questions = ?, keywords = ?, atoms = ?, card_model = ?, card_status = ?, indexed_at = ?",
		card.Title, card.Summary,
		joinList(card.Questions), joinList(card.Keywords), joinList(card.Atoms),
		cardModel, CardStatusOK, formatTime(now),
	)
}

// FailCard records that card generation failed. The heuristic card written at
// ingest stays in place so the record is still searchable.
func (s *Store) FailCard(ctx context.Context, recordID int64, finalizedAt *time.Time, now time.Time) (bool, error) {
	return s.updateQueuedCard(ctx, recordID, finalizedAt,
		"card_status = ?, indexed_at = ?",
		CardStatusFailed, formatTime(now),
	)
}

// KeepHeuristicCard ends card generation without an LLM and records that the
// ingest-time heuristic card is the final card. This is used when card
// generation is not configured, which is not an error.
func (s *Store) KeepHeuristicCard(ctx context.Context, recordID int64, finalizedAt *time.Time, now time.Time) (bool, error) {
	return s.updateQueuedCard(ctx, recordID, finalizedAt,
		"card_status = ?, indexed_at = ?",
		CardStatusHeuristic, formatTime(now),
	)
}

// updateQueuedCard applies a card transition only to the final version the job
// was generated for. finalized_at changes on every new final version, while a
// live capture leaves it alone.
func (s *Store) updateQueuedCard(ctx context.Context, recordID int64, finalizedAt *time.Time, setClause string, args ...any) (bool, error) {
	args = append(args, recordID, finalizationKey(finalizedAt), CardStatusQueued)
	result, err := s.db.ExecContext(ctx,
		`UPDATE records SET `+setClause+`
		 WHERE id = ? AND COALESCE(finalized_at, '') = ? AND card_status = ?`,
		args...,
	)
	if err != nil {
		return false, fmt.Errorf("update card for record %d: %w", recordID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update card for record %d: %w", recordID, err)
	}
	return affected > 0, nil
}

func finalizationKey(finalizedAt *time.Time) string {
	if finalizedAt == nil {
		return ""
	}
	return formatTime(*finalizedAt)
}

// GetRecord returns one full record by id. Missing records return ErrNotFound.
func (s *Store) GetRecord(ctx context.Context, id int64) (Record, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			r.id, r.client_id, r.source, r.external_id, COALESCE(p.key, ''),
			r.kind, r.title, r.summary, r.questions, r.keywords, r.atoms,
			r.raw_text, r.content_hash, r.revision, r.is_final, r.finalized_at,
			r.started_at, r.updated_at, r.indexed_at, r.card_model,
			r.card_status, r.metadata_json
		FROM records r
		LEFT JOIN projects p ON p.id = r.project_id
		WHERE r.id = ?`, id)

	record, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("get record %d: %w", id, err)
	}
	return record, nil
}

func scanRecord(scanner rowScanner) (Record, error) {
	var (
		record      Record
		isFinal     int
		questions   string
		keywords    string
		atoms       string
		finalizedAt sql.NullString
		startedAt   sql.NullString
		updatedAt   string
		indexedAt   string
		cardModel   sql.NullString
		metadata    string
	)
	if err := scanner.Scan(
		&record.ID, &record.ClientID, &record.Source, &record.ExternalID, &record.ProjectKey,
		&record.Kind, &record.Title, &record.Summary, &questions, &keywords, &atoms,
		&record.RawText, &record.ContentHash, &record.Revision, &isFinal, &finalizedAt,
		&startedAt, &updatedAt, &indexedAt, &cardModel,
		&record.CardStatus, &metadata,
	); err != nil {
		return Record{}, err
	}

	record.Questions = splitList(questions)
	record.Keywords = splitList(keywords)
	record.Atoms = splitList(atoms)
	record.IsFinal = isFinal != 0
	var err error
	record.FinalizedAt, err = parseNullableTime(finalizedAt)
	if err != nil {
		return Record{}, err
	}
	record.StartedAt, err = parseNullableTime(startedAt)
	if err != nil {
		return Record{}, err
	}
	record.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Record{}, err
	}
	record.IndexedAt, err = parseTime(indexedAt)
	if err != nil {
		return Record{}, err
	}
	if cardModel.Valid {
		record.CardModel = &cardModel.String
	}
	if err := json.Unmarshal([]byte(metadata), &record.Metadata); err != nil {
		return Record{}, fmt.Errorf("decode metadata: %w", err)
	}
	if record.Metadata == nil {
		record.Metadata = map[string]any{}
	}
	return record, nil
}
