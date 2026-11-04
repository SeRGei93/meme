package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/memory"
)

// ErrMemoryConflict is returned when an external_id is reused with different
// content. The caller must pick a new external_id.
var ErrMemoryConflict = errors.New("memory external_id already exists with different content")

// Memory statuses from the v1 contract.
const (
	MemoryStatusIndexed = "indexed"
	MemoryStatusQueued  = "queued"
)

// memoryOperation is the idempotency namespace for POST /api/v1/memories.
const memoryOperation = "memory"

// memoryTitleRunes caps the heuristic title taken from the first raw_text line.
const memoryTitleRunes = 200

// MemoryParams is one validated memory request ready to be persisted.
type MemoryParams struct {
	ClientID       int64
	Source         string
	IdempotencyKey string
	Request        memory.Request
	Now            time.Time
}

// MemoryResult is the response returned by POST /api/v1/memories.
type MemoryResult struct {
	RecordID int64
	JobID    *int64
	Status   string
	Replayed bool
}

// preparedMemory derives everything the store needs from the request once, so
// the content hash, external id and stored title cannot drift apart.
type preparedMemory struct {
	MemoryParams
	requestHash string
	contentHash string
	externalID  string
	title       string
}

func prepareMemory(params MemoryParams) preparedMemory {
	params.Now = params.Now.UTC()

	// The idempotency hash covers the exact request, so a retry that reuses a
	// key with a different body is rejected. The content hash is computed on
	// the masked copy, so captures that differ only in a secret value are the
	// same knowledge and do not conflict.
	requestHash := params.Request.RequestHash()
	request := params.Request.Masked()
	// Persist only the masked request: secrets must never reach SQLite or a
	// later LLM prompt.
	params.Request = request

	externalID := request.ExternalIDOrAuto()
	title := request.Title
	if title == "" {
		title = firstLine(request.RawText, memoryTitleRunes)
	}
	if title == "" {
		title = externalID
	}

	return preparedMemory{
		MemoryParams: params,
		requestHash:  requestHash,
		contentHash:  request.ContentHash(),
		externalID:   externalID,
		title:        title,
	}
}

// SaveMemory stores one memory. A complete card is written as-is with no card
// job; text-only memories are stored with a heuristic card and a queued
// generation job. The whole operation is one SQLite transaction.
func (s *Store) SaveMemory(ctx context.Context, params MemoryParams) (MemoryResult, error) {
	if params.Now.IsZero() {
		params.Now = time.Now().UTC()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MemoryResult{}, fmt.Errorf("begin memory save: %w", err)
	}

	result, err := saveMemoryTx(ctx, tx, prepareMemory(params))
	if err != nil {
		_ = tx.Rollback()
		return MemoryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MemoryResult{}, fmt.Errorf("commit memory save: %w", err)
	}
	return result, nil
}

func saveMemoryTx(ctx context.Context, tx *sql.Tx, params preparedMemory) (MemoryResult, error) {
	if replayed, ok, err := checkIdempotency(ctx, tx, params.ClientID, memoryOperation, params.IdempotencyKey, params.requestHash, params.Now); err != nil {
		return MemoryResult{}, err
	} else if ok {
		return MemoryResult{
			RecordID: replayed.RecordID,
			JobID:    replayed.JobID,
			Status:   replayed.Status,
			Replayed: true,
		}, nil
	}

	var (
		existingID   int64
		existingHash string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT id, content_hash
		FROM records
		WHERE client_id = ? AND source = ? AND external_id = ?`,
		params.ClientID, params.Source, params.externalID,
	).Scan(&existingID, &existingHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return insertMemoryRow(ctx, tx, params)
	case err != nil:
		return MemoryResult{}, fmt.Errorf("find memory record: %w", err)
	}

	if existingHash != params.contentHash {
		return MemoryResult{}, ErrMemoryConflict
	}
	return replayMemory(ctx, tx, existingID)
}

func replayMemory(ctx context.Context, tx *sql.Tx, recordID int64) (MemoryResult, error) {
	var cardStatus string
	if err := tx.QueryRowContext(ctx,
		"SELECT card_status FROM records WHERE id = ?", recordID,
	).Scan(&cardStatus); err != nil {
		return MemoryResult{}, fmt.Errorf("read memory record %d: %w", recordID, err)
	}

	status := MemoryStatusIndexed
	var jobID *int64
	if cardStatus == CardStatusQueued {
		status = MemoryStatusQueued
		latest, err := latestIngestJob(ctx, tx, recordID)
		if err != nil {
			return MemoryResult{}, err
		}
		jobID = latest
	}

	return MemoryResult{
		RecordID: recordID,
		JobID:    jobID,
		Status:   status,
		Replayed: true,
	}, nil
}

func insertMemoryRow(ctx context.Context, tx *sql.Tx, params preparedMemory) (MemoryResult, error) {
	request := params.Request
	hasCard := request.HasCard()

	projectID, err := upsertProject(ctx, tx, request.EffectiveProjectKey(), "", params.Now)
	if err != nil {
		return MemoryResult{}, err
	}

	metadata, err := marshalJSONMap(request.Metadata)
	if err != nil {
		return MemoryResult{}, err
	}

	cardStatus := CardStatusOK
	if !hasCard {
		cardStatus = CardStatusQueued
	}

	startedAt := params.Now
	if occurredAt := request.OccurredAt; occurredAt != nil {
		startedAt = occurredAt.UTC()
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO records (
			client_id, source, external_id, project_id, kind, title, summary,
			questions, keywords, atoms, raw_text, content_hash, revision,
			is_final, finalized_at, started_at, updated_at, indexed_at,
			card_model, card_status, metadata_json
		) VALUES (?, ?, ?, ?, 'memory', ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, NULL, ?, ?)`,
		params.ClientID, params.Source, params.externalID, projectID,
		params.title, request.Summary,
		joinList(request.Questions), joinList(request.Keywords), joinList(request.Atoms),
		request.RawText, params.contentHash,
		formatTime(params.Now), formatTime(startedAt), formatTime(params.Now), formatTime(params.Now),
		cardStatus, metadata,
	)
	if err != nil {
		return MemoryResult{}, fmt.Errorf("insert memory record: %w", err)
	}

	recordID, err := result.LastInsertId()
	if err != nil {
		return MemoryResult{}, fmt.Errorf("insert memory record: %w", err)
	}

	status := MemoryStatusIndexed
	var jobID *int64
	if !hasCard {
		id, err := insertCardJob(ctx, tx, params.ClientID, params.IdempotencyKey, params.Source, params.externalID, recordID, params.Now, "memory")
		if err != nil {
			return MemoryResult{}, fmt.Errorf("enqueue memory job: %w", err)
		}
		jobID = &id
		status = MemoryStatusQueued
	}

	if err := saveIdempotency(ctx, tx, params.ClientID, memoryOperation, params.IdempotencyKey,
		params.requestHash, recordID, jobID, status, params.Now); err != nil {
		return MemoryResult{}, err
	}

	return MemoryResult{
		RecordID: recordID,
		JobID:    jobID,
		Status:   status,
		Replayed: false,
	}, nil
}

// firstLine returns the first line of raw text as a heuristic title. The
// fallback keeps the record named even when raw_text starts with blank lines.
func firstLine(value string, max int) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	value = strings.TrimSpace(strings.TrimSuffix(value, "\r"))
	if value == "" {
		return ""
	}
	if utf8.RuneCountInString(value) > max {
		value = string([]rune(value)[:max])
	}
	return value
}

func marshalJSONMap(value map[string]any) (string, error) {
	if value == nil {
		value = map[string]any{}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal metadata: %w", err)
	}
	return string(raw), nil
}
