package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/store"
)

const (
	graphPath        = "/api/v1/graph"
	graphMaxKeyRunes = 500
)

type graphResponse struct {
	Nodes     []graphNodeResponse `json:"nodes"`
	Edges     []graphEdgeResponse `json:"edges"`
	Truncated bool                `json:"truncated"`
}

type graphNodeResponse struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	ProjectKey string    `json:"project_key"`
	Source     string    `json:"source"`
	Kind       string    `json:"kind"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type graphEdgeResponse struct {
	FromID int64   `json:"from_id"`
	ToID   int64   `json:"to_id"`
	Weight float64 `json:"weight"`
	Reason string  `json:"reason"`
}

func (s *server) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	params, ok := parseGraphParams(w, r)
	if !ok {
		return
	}

	graph, err := s.store.Graph(r.Context(), params)
	switch {
	case errors.Is(err, store.ErrInvalidGraphQuery):
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	case err != nil:
		s.logger.Error("read graph",
			"error", err,
			"request_id", requestIDFrom(r.Context()),
		)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		return
	}

	nodes := make([]graphNodeResponse, len(graph.Nodes))
	for i, node := range graph.Nodes {
		nodes[i] = graphNodeResponse{
			ID:         node.ID,
			Title:      node.Title,
			ProjectKey: node.ProjectKey,
			Source:     node.Source,
			Kind:       node.Kind,
			UpdatedAt:  node.UpdatedAt,
		}
	}
	edges := make([]graphEdgeResponse, len(graph.Edges))
	for i, edge := range graph.Edges {
		edges[i] = graphEdgeResponse{
			FromID: edge.FromID,
			ToID:   edge.ToID,
			Weight: edge.Weight,
			Reason: edge.Reason,
		}
	}

	writeJSON(w, http.StatusOK, graphResponse{
		Nodes:     nodes,
		Edges:     edges,
		Truncated: graph.Truncated,
	})
}

func parseGraphParams(w http.ResponseWriter, r *http.Request) (store.GraphParams, bool) {
	query := r.URL.Query()
	invalid := func(field, message string) (store.GraphParams, bool) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", message, map[string]any{"field": field})
		return store.GraphParams{}, false
	}

	limit, ok := parseLimitParam(w, r, "limit", store.DefaultGraphLimit, store.MaxGraphLimit)
	if !ok {
		return store.GraphParams{}, false
	}

	minWeight := 0.0
	if values, present := query["min_weight"]; present {
		if len(values) != 1 || values[0] == "" {
			return invalid("min_weight", "min_weight must be a number between 0 and 1")
		}
		parsed, err := strconv.ParseFloat(values[0], 64)
		if err != nil || math.IsNaN(parsed) || parsed < 0 || parsed > 1 {
			return invalid("min_weight", "min_weight must be a number between 0 and 1")
		}
		minWeight = parsed
	}

	projectKey := strings.TrimSpace(query.Get("project_key"))
	if query.Has("project_key") && projectKey == "" {
		return invalid("project_key", "project_key must not be empty")
	}
	if utf8.RuneCountInString(projectKey) > graphMaxKeyRunes {
		return invalid("project_key", "project_key must be at most "+strconv.Itoa(graphMaxKeyRunes)+" characters")
	}

	source := strings.TrimSpace(query.Get("source"))
	if query.Has("source") && source == "" {
		return invalid("source", "source must not be empty")
	}
	if utf8.RuneCountInString(source) > auth.MaxSourceQueryLen {
		return invalid("source", "source must be at most "+strconv.Itoa(auth.MaxSourceQueryLen)+" characters")
	}

	return store.GraphParams{
		ProjectKey: projectKey,
		Source:     source,
		Limit:      limit,
		MinWeight:  minWeight,
	}, true
}
