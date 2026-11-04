package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/SeRGei93/meme/server/internal/store"
)

const ingestPath = "/api/v1/ingest"

type ingestResponse struct {
	RecordID int64  `json:"record_id"`
	JobID    *int64 `json:"job_id"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

func (s *server) handleIngest(w http.ResponseWriter, r *http.Request) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		s.writeUnauthorized(w, r)
		return
	}
	if principal.Client == nil || principal.Client.Kind != auth.KindOpenCode {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"only opencode clients may ingest session bundles",
			map[string]any{"required_kind": "opencode"})
		return
	}
	source, ok := principal.Client.Source()
	if !ok {
		writeError(w, r, http.StatusForbidden, "forbidden",
			"client kind cannot produce a knowledge source",
			map[string]any{"required_kind": "opencode"})
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	bundle, err := ingest.DecodeBundle(r.Body)
	if err != nil {
		writeJSONDecodeError(w, r, err)
		return
	}
	if err := bundle.Validate(); err != nil {
		var validationError *ingest.ValidationError
		if errors.As(err, &validationError) {
			writeError(w, r, http.StatusUnprocessableEntity, "validation_failed",
				validationError.Error(),
				map[string]any{"field": validationError.Field})
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
		return
	}

	idempotencyKey, ok := idempotencyKey(r, bundle.BundleID)
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			"Idempotency-Key must be 1-128 printable ASCII characters",
			map[string]any{"field": "Idempotency-Key"})
		return
	}

	result, err := s.store.IngestSession(r.Context(), store.IngestParams{
		ClientID:       principal.Client.ID,
		Source:         source,
		IdempotencyKey: idempotencyKey,
		Bundle:         bundle,
		Now:            time.Now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		writeError(w, r, http.StatusConflict, "idempotency_key_reused",
			"this Idempotency-Key was already used with a different request body", nil)
		return
	case err != nil:
		s.logger.Error("ingest session",
			"error", err,
			"client_id", principal.Client.ID,
			"external_id", bundle.ExternalID,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	status := http.StatusAccepted
	if result.Replayed {
		status = http.StatusOK
	}

	s.logger.Info("session ingested",
		"client_id", principal.Client.ID,
		"record_id", result.RecordID,
		"job_id", result.JobID,
		"replayed", result.Replayed,
		"request_id", requestIDFrom(r.Context()),
	)
	writeJSON(w, status, ingestResponse{
		RecordID: result.RecordID,
		JobID:    result.JobID,
		Status:   result.Status,
		Replayed: result.Replayed,
	})
}

// idempotencyKey returns the Idempotency-Key header when present and the
// endpoint's content-derived fallback otherwise.
func idempotencyKey(r *http.Request, fallback string) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return fallback, validIdempotencyKey(fallback)
	}
	if len(values) > 1 {
		return "", false
	}
	return values[0], validIdempotencyKey(values[0])
}

func validIdempotencyKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}
