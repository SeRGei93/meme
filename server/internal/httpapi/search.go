package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/search"
	"github.com/SeRGei93/meme/server/internal/store"
)

const (
	searchPath        = "/api/v1/search"
	searchMaxParamLen = 500
)

type searchResponse struct {
	Items []searchHitResponse `json:"items"`
	Total int                 `json:"total"`
	Query string              `json:"query"`
	Limit int                 `json:"limit"`
}

type searchHitResponse struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	Kind        string     `json:"kind"`
	ProjectKey  string     `json:"project_key"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Questions   []string   `json:"questions"`
	Keywords    []string   `json:"keywords"`
	Atoms       []string   `json:"atoms"`
	IsFinal     bool       `json:"is_final"`
	StartedAt   *time.Time `json:"started_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	FinalizedAt *time.Time `json:"finalized_at"`
	CardStatus  string     `json:"card_status"`
	CardModel   *string    `json:"card_model"`
	Score       float64    `json:"score"`
}

func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	params, ok := parseSearchParams(w, r)
	if !ok {
		return
	}

	result, err := s.store.Search(r.Context(), params)
	switch {
	case errors.Is(err, store.ErrInvalidSearch):
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	case err != nil:
		s.logger.Error("search",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	writeJSON(w, http.StatusOK, toSearchResponse(result, params.Query, params.Limit))
}

// toSearchResponse is shared by REST and MCP so both surfaces serialize the
// same fields.
func toSearchResponse(result store.SearchResult, query string, limit int) searchResponse {
	items := make([]searchHitResponse, len(result.Items))
	for i, hit := range result.Items {
		items[i] = toSearchHitResponse(hit)
	}
	return searchResponse{
		Items: items,
		Total: result.Total,
		Query: query,
		Limit: limit,
	}
}

func parseSearchParams(w http.ResponseWriter, r *http.Request) (store.SearchParams, bool) {
	query := r.URL.Query()
	invalid := func(field, message string) (store.SearchParams, bool) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", message, map[string]any{"field": field})
		return store.SearchParams{}, false
	}

	q := strings.TrimSpace(query.Get("q"))
	if q == "" {
		return invalid("q", "q is required")
	}
	if utf8.RuneCountInString(q) > searchMaxParamLen {
		return invalid("q", "q must be at most "+strconv.Itoa(searchMaxParamLen)+" characters")
	}

	limit := search.DefaultLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > search.MaxLimit {
			return invalid("limit", "limit must be an integer between 1 and "+strconv.Itoa(search.MaxLimit))
		}
		limit = parsed
	}

	projectKey := strings.TrimSpace(query.Get("project_key"))
	if query.Has("project_key") && projectKey == "" {
		return invalid("project_key", "project_key must not be empty")
	}
	if utf8.RuneCountInString(projectKey) > searchMaxParamLen {
		return invalid("project_key", "project_key must be at most "+strconv.Itoa(searchMaxParamLen)+" characters")
	}

	source := query.Get("source")
	if query.Has("source") && source == "" {
		return invalid("source", "source must be opencode or jarvis")
	}
	if source != "" && !auth.ValidSource(source) {
		return invalid("source", "source must be opencode or jarvis")
	}

	since, ok := parseSearchTime(w, r, query.Get("since"), "since")
	if !ok {
		return store.SearchParams{}, false
	}
	until, ok := parseSearchTime(w, r, query.Get("until"), "until")
	if !ok {
		return store.SearchParams{}, false
	}
	if since != nil && until != nil && since.After(*until) {
		return invalid("since", "since must not be after until")
	}

	return store.SearchParams{
		Query:      q,
		Limit:      limit,
		ProjectKey: projectKey,
		Source:     source,
		Since:      since,
		Until:      until,
	}, true
}

func parseSearchTime(w http.ResponseWriter, r *http.Request, raw, field string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request",
			field+" must be an RFC 3339 date-time",
			map[string]any{"field": field})
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func toSearchHitResponse(hit store.SearchHit) searchHitResponse {
	return searchHitResponse{
		ID:          hit.ID,
		Source:      hit.Source,
		Kind:        hit.Kind,
		ProjectKey:  hit.ProjectKey,
		Title:       hit.Title,
		Summary:     hit.Summary,
		Questions:   hit.Questions,
		Keywords:    hit.Keywords,
		Atoms:       hit.Atoms,
		IsFinal:     hit.IsFinal,
		StartedAt:   hit.StartedAt,
		UpdatedAt:   hit.UpdatedAt,
		FinalizedAt: hit.FinalizedAt,
		CardStatus:  hit.CardStatus,
		CardModel:   hit.CardModel,
		Score:       hit.Score,
	}
}
