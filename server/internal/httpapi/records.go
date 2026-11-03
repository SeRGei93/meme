package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/store"
)

const recordsPath = "/api/v1/records"

type recordResponse struct {
	ID          int64          `json:"id"`
	Source      string         `json:"source"`
	Kind        string         `json:"kind"`
	ProjectKey  string         `json:"project_key"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Questions   []string       `json:"questions"`
	Keywords    []string       `json:"keywords"`
	Atoms       []string       `json:"atoms"`
	IsFinal     bool           `json:"is_final"`
	StartedAt   *time.Time     `json:"started_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	FinalizedAt *time.Time     `json:"finalized_at"`
	CardStatus  string         `json:"card_status"`
	CardModel   *string        `json:"card_model"`
	RawText     string         `json:"raw_text"`
	Revision    int64          `json:"revision"`
	Metadata    map[string]any `json:"metadata"`
	IndexedAt   time.Time      `json:"indexed_at"`
}

func (s *server) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimPrefix(r.URL.Path, recordsPath+"/")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"record id must be a positive integer",
			map[string]any{"field": "id"})
		return
	}

	record, err := s.store.GetRecord(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "record not found", nil)
		return
	case err != nil:
		s.logger.Error("get record",
			"error", err,
			"record_id", id,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, toRecordResponse(record))
}

func toRecordResponse(record store.Record) recordResponse {
	return recordResponse{
		ID:          record.ID,
		Source:      record.Source,
		Kind:        record.Kind,
		ProjectKey:  record.ProjectKey,
		Title:       record.Title,
		Summary:     record.Summary,
		Questions:   record.Questions,
		Keywords:    record.Keywords,
		Atoms:       record.Atoms,
		IsFinal:     record.IsFinal,
		StartedAt:   record.StartedAt,
		UpdatedAt:   record.UpdatedAt,
		FinalizedAt: record.FinalizedAt,
		CardStatus:  record.CardStatus,
		CardModel:   record.CardModel,
		RawText:     record.RawText,
		Revision:    record.Revision,
		Metadata:    record.Metadata,
		IndexedAt:   record.IndexedAt,
	}
}
