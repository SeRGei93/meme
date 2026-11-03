package httpapi

import (
	"context"
	"net/http"
	"time"
)

type healthResponse struct {
	Status        string         `json:"status"`
	Version       string         `json:"version"`
	SchemaVersion int            `json:"schema_version"`
	Time          time.Time      `json:"time"`
	Database      healthDatabase `json:"database"`
}

type healthDatabase struct {
	Status string `json:"status"`
	FTS5   bool   `json:"fts5"`
	Error  string `json:"error,omitempty"`
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	health, err := s.store.Health(ctx)
	response := healthResponse{
		Status:        "ok",
		Version:       s.version,
		SchemaVersion: health.SchemaVersion,
		Time:          time.Now().UTC(),
		Database: healthDatabase{
			Status: "ok",
			FTS5:   health.FTS5,
		},
	}
	status := http.StatusOK

	if err != nil {
		status = http.StatusServiceUnavailable
		response.Status = "degraded"
		response.Database.Status = "error"
		response.Database.Error = err.Error()
	} else if !health.FTS5 {
		status = http.StatusServiceUnavailable
		response.Status = "degraded"
		response.Database.Status = "error"
		response.Database.Error = "FTS5 is not available"
	}

	writeJSON(w, status, response)
}
