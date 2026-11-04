package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/store"
)

const jobsPath = "/api/v1/jobs"

type jobListResponse struct {
	Items      []jobResponse `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type jobResponse struct {
	ID          int64      `json:"id"`
	ClientID    int64      `json:"client_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	ExternalID  string     `json:"external_id"`
	RecordID    *int64     `json:"record_id"`
	NextRetryAt *time.Time `json:"next_retry_at"`
	Error       *string    `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	params, ok := parseJobParams(w, r)
	if !ok {
		return
	}

	page, err := s.store.ListJobs(r.Context(), params)
	switch {
	case errors.Is(err, store.ErrInvalidJobQuery):
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	case err != nil:
		s.logger.Error("list jobs",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	items := make([]jobResponse, len(page.Items))
	for i, job := range page.Items {
		items[i] = toJobResponse(job)
	}

	var nextCursor *string
	if page.HasMore && len(page.Items) > 0 {
		cursor := encodeJobCursor(page.Items[len(page.Items)-1].ID)
		nextCursor = &cursor
	}

	writeJSON(w, http.StatusOK, jobListResponse{Items: items, NextCursor: nextCursor})
}

func parseJobParams(w http.ResponseWriter, r *http.Request) (store.ListJobsParams, bool) {
	query := r.URL.Query()
	invalid := func(field, message string) (store.ListJobsParams, bool) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", message, map[string]any{"field": field})
		return store.ListJobsParams{}, false
	}

	var status store.JobStatus
	if query.Has("status") {
		status = store.JobStatus(query.Get("status"))
		if !status.Valid() {
			return invalid("status", "status must be queued, running, done or failed")
		}
	}

	limit := store.DefaultJobLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > store.MaxJobLimit {
			return invalid("limit", "limit must be an integer between 1 and "+strconv.Itoa(store.MaxJobLimit))
		}
		limit = parsed
	}

	var beforeID int64
	if query.Has("cursor") {
		decoded, err := decodeJobCursor(query.Get("cursor"))
		if err != nil {
			return invalid("cursor", "cursor is invalid")
		}
		beforeID = decoded
	}

	return store.ListJobsParams{Status: status, Limit: limit, BeforeID: beforeID}, true
}

func toJobResponse(job store.Job) jobResponse {
	var errText *string
	if job.Error != "" {
		value := job.Error
		errText = &value
	}
	return jobResponse{
		ID:          job.ID,
		ClientID:    job.ClientID,
		Kind:        job.Kind,
		Status:      string(job.Status),
		Attempts:    job.Attempts,
		ExternalID:  job.ExternalID,
		RecordID:    job.RecordID,
		NextRetryAt: job.NextRetryAt,
		Error:       errText,
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
	}
}

// Job cursors are opaque to clients: base64url of the last id on the page.
func encodeJobCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

func decodeJobCursor(raw string) (int64, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(string(decoded), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("invalid job cursor")
	}
	return id, nil
}
