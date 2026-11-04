package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/graph"
)

// ErrInvalidGraphQuery is returned when a graph request violates the v1
// contract.
var ErrInvalidGraphQuery = errors.New("invalid graph query")

const (
	// DefaultGraphLimit is the contract's node cap when limit is omitted.
	DefaultGraphLimit = 100
	// MaxGraphLimit is the contract's upper bound for GET /api/v1/graph?limit.
	MaxGraphLimit = 500
	// MaxGraphEdgesPerNode bounds the graph density so a large project cannot
	// turn into a complete graph that the UI cannot render.
	MaxGraphEdgesPerNode = graph.MaxEdgesPerNode
)

// GraphEdge is one link between two records from the graph rule set.
type GraphEdge = graph.Edge

// GraphParams filters the memory graph. Zero values mean "not set": Limit
// defaults to 100, ProjectKey matches every project and MinWeight is 0.
type GraphParams struct {
	ProjectKey string
	Limit      int
	MinWeight  float64
}

// GraphNode is one memory record rendered as a graph node.
type GraphNode struct {
	ID         int64
	Title      string
	ProjectKey string
	Source     string
	Kind       string
	UpdatedAt  time.Time
}

// Graph is the response of GET /api/v1/graph. Truncated reports that the node
// limit or the per-node edge cap removed part of the graph.
type Graph struct {
	Nodes     []GraphNode
	Edges     []GraphEdge
	Truncated bool
}

// Graph returns the most recently updated records as nodes and links them by
// shared project and keyword overlap. Edges carry their strength in Weight and
// never exceed MaxGraphEdgesPerNode per node.
func (s *Store) Graph(ctx context.Context, params GraphParams) (Graph, error) {
	limit := params.Limit
	if limit == 0 {
		limit = DefaultGraphLimit
	}
	if limit < 1 || limit > MaxGraphLimit {
		return Graph{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidGraphQuery, MaxGraphLimit)
	}
	if params.MinWeight < 0 || params.MinWeight > 1 {
		return Graph{}, fmt.Errorf("%w: min_weight must be between 0 and 1", ErrInvalidGraphQuery)
	}
	projectKey := strings.TrimSpace(params.ProjectKey)
	if utf8.RuneCountInString(projectKey) > 500 {
		return Graph{}, fmt.Errorf("%w: project_key must be at most 500 characters", ErrInvalidGraphQuery)
	}

	// Fetch one extra row so truncation is detected without a COUNT.
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			r.id, r.title, COALESCE(p.key, ''), r.source, r.kind,
			r.updated_at, r.project_id, r.keywords
		FROM records r
		LEFT JOIN projects p ON p.id = r.project_id
		WHERE (? = '' OR p.key = ?)
		ORDER BY r.updated_at DESC, r.id DESC
		LIMIT ?`,
		projectKey, projectKey, limit+1,
	)
	if err != nil {
		return Graph{}, fmt.Errorf("list graph nodes: %w", err)
	}
	defer rows.Close()

	nodes := []GraphNode{}
	records := []graph.Record{}
	for rows.Next() {
		var (
			node      GraphNode
			projectID sql.NullInt64
			keywords  string
			updatedAt string
		)
		if err := rows.Scan(
			&node.ID, &node.Title, &node.ProjectKey, &node.Source, &node.Kind,
			&updatedAt, &projectID, &keywords,
		); err != nil {
			return Graph{}, fmt.Errorf("scan graph node: %w", err)
		}
		node.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return Graph{}, fmt.Errorf("scan graph node %d: %w", node.ID, err)
		}

		record := graph.Record{ID: node.ID, Keywords: splitList(keywords)}
		if projectID.Valid {
			record.ProjectID = projectID.Int64
		}
		nodes = append(nodes, node)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return Graph{}, fmt.Errorf("list graph nodes: %w", err)
	}

	truncated := false
	if len(nodes) > limit {
		nodes = nodes[:limit]
		records = records[:limit]
		truncated = true
	}

	edges, capped := graph.Build(records, params.MinWeight, MaxGraphEdgesPerNode)
	if edges == nil {
		edges = []GraphEdge{}
	}

	return Graph{Nodes: nodes, Edges: edges, Truncated: truncated || capped}, nil
}
