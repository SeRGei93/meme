package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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
	MaxGraphEdgesPerNode = 20

	// A shared project is a weak signal; keyword overlap is the strong one. Both
	// together reach the contract's maximum weight of 1.
	projectEdgeWeight = 0.25
	keywordEdgeWeight = 0.75
)

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

// Graph reasons from the v1 contract.
const (
	GraphReasonProject  = "project"
	GraphReasonKeywords = "keywords"
)

// GraphEdge links two records. FromID is always smaller than ToID.
type GraphEdge struct {
	FromID int64
	ToID   int64
	Weight float64
	Reason string
}

// Graph is the response of GET /api/v1/graph. Truncated reports that the node
// limit or the per-node edge cap removed part of the graph.
type Graph struct {
	Nodes     []GraphNode
	Edges     []GraphEdge
	Truncated bool
}

type graphRecord struct {
	node      GraphNode
	projectID int64
	keywords  map[string]struct{}
}

// Graph returns the most recently updated records as nodes and links them by
// shared project and keyword overlap. Edges keep the strongest links first and
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

	records := []graphRecord{}
	for rows.Next() {
		var (
			record    graphRecord
			projectID sql.NullInt64
			keywords  string
			updatedAt string
		)
		if err := rows.Scan(
			&record.node.ID, &record.node.Title, &record.node.ProjectKey,
			&record.node.Source, &record.node.Kind, &updatedAt,
			&projectID, &keywords,
		); err != nil {
			rows.Close()
			return Graph{}, fmt.Errorf("scan graph node: %w", err)
		}
		record.node.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			rows.Close()
			return Graph{}, fmt.Errorf("scan graph node %d: %w", record.node.ID, err)
		}
		if projectID.Valid {
			record.projectID = projectID.Int64
		}
		record.keywords = keywordSet(keywords)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Graph{}, fmt.Errorf("list graph nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Graph{}, fmt.Errorf("list graph nodes: %w", err)
	}

	truncated := false
	if len(records) > limit {
		records = records[:limit]
		truncated = true
	}

	graph := Graph{
		Nodes:     make([]GraphNode, len(records)),
		Edges:     []GraphEdge{},
		Truncated: truncated,
	}
	for i, record := range records {
		graph.Nodes[i] = record.node
	}

	edges, capped := buildGraphEdges(records, params.MinWeight)
	graph.Edges = edges
	graph.Truncated = graph.Truncated || capped
	return graph, nil
}

// buildGraphEdges scores every record pair, drops links below minWeight and
// keeps each node's strongest links. The selection is two-pass: first every
// node nominates its top links, then nodes above the cap shed their weakest
// kept links, so a dense project cannot starve the newest records.
func buildGraphEdges(records []graphRecord, minWeight float64) ([]GraphEdge, bool) {
	candidates := []GraphEdge{}
	for i := range records {
		for j := i + 1; j < len(records); j++ {
			weight, reason := graphLinkWeight(records[i], records[j])
			if weight == 0 || weight < minWeight {
				continue
			}
			from, to := records[i].node.ID, records[j].node.ID
			if from > to {
				from, to = to, from
			}
			candidates = append(candidates, GraphEdge{
				FromID: from,
				ToID:   to,
				Weight: weight,
				Reason: reason,
			})
		}
	}

	// Strongest first, with a stable tie-break so equal weights still produce a
	// deterministic graph.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Weight != candidates[j].Weight {
			return candidates[i].Weight > candidates[j].Weight
		}
		if candidates[i].FromID != candidates[j].FromID {
			return candidates[i].FromID < candidates[j].FromID
		}
		return candidates[i].ToID < candidates[j].ToID
	})

	incident := make(map[int64][]int, len(records)*2)
	for i, edge := range candidates {
		incident[edge.FromID] = append(incident[edge.FromID], i)
		incident[edge.ToID] = append(incident[edge.ToID], i)
	}

	selected := make([]bool, len(candidates))
	degree := make(map[int64]int, len(records))
	for _, list := range incident {
		for _, index := range list[:min(len(list), MaxGraphEdgesPerNode)] {
			if selected[index] {
				continue
			}
			selected[index] = true
			degree[candidates[index].FromID]++
			degree[candidates[index].ToID]++
		}
	}

	// A node can exceed the cap when many neighbours nominate it. Shed its
	// weakest kept links, preferring links to other over-cap nodes, so pruning
	// removes redundant edges instead of starving the newest records.
	incidentOrder := make(map[int64][]int, len(incident))
	for nodeID, list := range incident {
		ordered := append([]int(nil), list...)
		sort.SliceStable(ordered, func(i, j int) bool {
			first, second := candidates[ordered[i]], candidates[ordered[j]]
			if first.Weight != second.Weight {
				return first.Weight < second.Weight
			}
			return otherNode(first, nodeID) > otherNode(second, nodeID)
		})
		incidentOrder[nodeID] = ordered
	}

	for {
		var (
			overNode int64
			overDeg  = MaxGraphEdgesPerNode
		)
		for nodeID, d := range degree {
			if d > overDeg {
				overNode, overDeg = nodeID, d
			}
		}
		if overNode == 0 {
			break
		}

		pick := -1
		for _, index := range incidentOrder[overNode] {
			if !selected[index] {
				continue
			}
			edge := candidates[index]
			if degree[otherNode(edge, overNode)] > MaxGraphEdgesPerNode {
				pick = index
				break
			}
			if pick == -1 {
				pick = index
			}
		}
		if pick == -1 {
			break
		}
		selected[pick] = false
		degree[candidates[pick].FromID]--
		degree[candidates[pick].ToID]--
	}

	kept := make([]GraphEdge, 0, len(candidates))
	for i, edge := range candidates {
		if selected[i] {
			kept = append(kept, edge)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].FromID != kept[j].FromID {
			return kept[i].FromID < kept[j].FromID
		}
		return kept[i].ToID < kept[j].ToID
	})

	return kept, len(kept) < len(candidates)
}

// otherNode returns the endpoint of edge that is not nodeID.
func otherNode(edge GraphEdge, nodeID int64) int64 {
	if edge.FromID == nodeID {
		return edge.ToID
	}
	return edge.FromID
}

// graphLinkWeight combines the two v1 link signals. Reason names the stronger
// contributor, so a pair that shares both is reported as a keyword link.
func graphLinkWeight(first, second graphRecord) (float64, string) {
	weight := 0.0
	reason := ""

	if first.projectID != 0 && first.projectID == second.projectID {
		weight = projectEdgeWeight
		reason = GraphReasonProject
	}
	if overlap := jaccard(first.keywords, second.keywords); overlap > 0 {
		weight += keywordEdgeWeight * overlap
		reason = GraphReasonKeywords
	}
	if weight > 1 {
		weight = 1
	}
	return weight, reason
}

// keywordSet normalizes the newline-separated keywords column for overlap
// comparison: trimmed, lower-cased and de-duplicated.
func keywordSet(raw string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, keyword := range strings.Split(raw, "\n") {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		set[keyword] = struct{}{}
	}
	return set
}

func jaccard(first, second map[string]struct{}) float64 {
	if len(first) == 0 || len(second) == 0 {
		return 0
	}

	shared := 0
	for keyword := range first {
		if _, ok := second[keyword]; ok {
			shared++
		}
	}
	union := len(first) + len(second) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}
