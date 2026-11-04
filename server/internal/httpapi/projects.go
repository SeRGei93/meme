package httpapi

import (
	"errors"
	"net/http"
	"strconv"
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

// parseLimitParam reads a bounded integer query parameter that defaults to def
// when omitted. On failure it writes the contract's invalid_request error.
func parseLimitParam(w http.ResponseWriter, r *http.Request, name string, def, max int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}

	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 || parsed > max {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			name+" must be an integer between 1 and "+strconv.Itoa(max),
			map[string]any{"field": name})
		return 0, false
	}
	return parsed, true
}
