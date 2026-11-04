package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/SeRGei93/meme/server/internal/store"
)

const projectsPath = "/api/v1/projects"

type projectListResponse struct {
	Items []projectResponse `json:"items"`
}

type projectResponse struct {
	Key            string     `json:"key"`
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	RecordCount    int        `json:"record_count"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (s *server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimitParam(w, r, "limit", store.DefaultProjectLimit, store.MaxProjectLimit)
	if !ok {
		return
	}

	projects, err := s.store.ListProjects(r.Context(), store.ListProjectsParams{Limit: limit})
	switch {
	case errors.Is(err, store.ErrInvalidProjectQuery):
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	case err != nil:
		s.logger.Error("list projects",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	items := make([]projectResponse, len(projects))
	for i, project := range projects {
		items[i] = projectResponse{
			Key:            project.Key,
			Name:           project.Name,
			Path:           project.Path,
			RecordCount:    project.RecordCount,
			LastActivityAt: project.LastActivityAt,
			CreatedAt:      project.CreatedAt,
		}
	}

	writeJSON(w, http.StatusOK, projectListResponse{Items: items})
}
