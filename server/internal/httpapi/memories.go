package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/store"
)

const memoriesPath = "/api/v1/memories"

type memoryResponse struct {
	RecordID int64  `json:"record_id"`
	JobID    *int64 `json:"job_id"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

func (s *server) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.writeUnauthorized(w, r)
		return
	}
	if principal.Client == nil {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"the bootstrap admin token cannot write memories",
			map[string]any{"required_kind": "opencode or jarvis"})
		return
	}
	source, ok := principal.Client.Source()
	if !ok {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"client kind cannot produce a knowledge source",
			map[string]any{"required_kind": "opencode or jarvis"})
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	request, err := memory.Decode(r.Body)
	if err != nil {
		if writeMemoryValidationError(w, r, err) {
			return
		}
		writeJSONDecodeError(w, r, err)
		return
	}
	if err := request.Validate(); err != nil {
		if !writeMemoryValidationError(w, r, err) {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		}
		return
	}

	idempotencyKey, ok := idempotencyKey(r, request.RequestHash())
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"Idempotency-Key must be 1-128 printable ASCII characters",
			map[string]any{"field": "Idempotency-Key"})
		return
	}

	result, err := s.store.SaveMemory(r.Context(), store.MemoryParams{
		ClientID:       principal.Client.ID,
		Source:         source,
		IdempotencyKey: idempotencyKey,
		Request:        request,
		Now:            time.Now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrMemoryConflict):
		writeError(w, r, http.StatusConflict, "conflict",
			"external_id already exists with different content",
			map[string]any{"field": "external_id"})
		return
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		writeError(w, r, http.StatusConflict, "idempotency_key_reused",
			"this Idempotency-Key was already used with a different request body", nil)
		return
	case err != nil:
		s.logger.Error("save memory",
			"error", err,
			"client_id", principal.Client.ID,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}

	s.logger.Info("memory saved",
		"client_id", principal.Client.ID,
		"record_id", result.RecordID,
		"job_id", result.JobID,
		"status", result.Status,
		"replayed", result.Replayed,
		"request_id", requestIDFrom(r.Context()),
	)
	writeJSON(w, status, memoryResponse{
		RecordID: result.RecordID,
		JobID:    result.JobID,
		Status:   result.Status,
		Replayed: result.Replayed,
	})
}

// writeMemoryValidationError maps a memory schema violation to 422 and reports
// whether it handled the error.
func writeMemoryValidationError(w http.ResponseWriter, r *http.Request, err error) bool {
	var validationError *memory.ValidationError
	if !errors.As(err, &validationError) {
		return false
	}
	writeError(w, r, http.StatusUnprocessableEntity, "validation_failed",
		validationError.Error(),
		map[string]any{"field": validationError.Field})
	return true
}
