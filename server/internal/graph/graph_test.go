package graph_test

import (
	"testing"

	"github.com/SeRGei93/meme/server/internal/graph"
)

func TestBuildLinksRecordsBySharedProjectAndKeywords(t *testing.T) {
	records := []graph.Record{
		{ID: 1, ProjectID: 10, Keywords: []string{"sqlite", "fts5", "search"}},
		{ID: 2, ProjectID: 10, Keywords: []string{"sqlite", "fts5"}},
		{ID: 3, ProjectID: 20, Keywords: []string{"sqlite"}},
	}

	edges, capped := graph.Build(records, 0, graph.MaxEdgesPerNode)
	if capped {
		t.Error("capped = true, want false for a sparse graph")
	}
	if len(edges) != 3 {
		t.Fatalf("edges = %d, want 3: %+v", len(edges), edges)
	}
	assertEdge(t, edges, 1, 2, graph.ReasonKeywords, 0.25+0.75*2.0/3.0)
	assertEdge(t, edges, 1, 3, graph.ReasonKeywords, 0.75/3.0)
	assertEdge(t, edges, 2, 3, graph.ReasonKeywords, 0.75/2.0)
}

func TestBuildNamesTheLargerContributorAsReason(t *testing.T) {
	// The shared project (0.25) outweighs one shared keyword out of four
	// (0.75 * 1/4 = 0.1875), so the reason stays project.
	records := []graph.Record{
		{ID: 1, ProjectID: 10, Keywords: []string{"a", "b", "c", "d"}},
		{ID: 2, ProjectID: 10, Keywords: []string{"a"}},
	}

	edges, _ := graph.Build(records, 0, graph.MaxEdgesPerNode)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want one link", edges)
	}
	assertEdge(t, edges, 1, 2, graph.ReasonProject, 0.25+0.75*0.25)

	// A dominant keyword match switches the reason even when the project is
	// shared.
	records[1].Keywords = []string{"a", "b", "c"}
	edges, _ = graph.Build(records, 0, graph.MaxEdgesPerNode)
	assertEdge(t, edges, 1, 2, graph.ReasonKeywords, 0.25+0.75*0.75)
}

func TestBuildNormalizesKeywordCaseAndWhitespace(t *testing.T) {
	records := []graph.Record{
		{ID: 1, Keywords: []string{" SQLite ", "FTS5"}},
		{ID: 2, Keywords: []string{"sqlite", "fts5"}},
	}

	edges, _ := graph.Build(records, 0, graph.MaxEdgesPerNode)
	assertEdge(t, edges, 1, 2, graph.ReasonKeywords, 0.75)
}

func TestBuildDropsLinksBelowMinWeight(t *testing.T) {
	records := []graph.Record{
		{ID: 1, ProjectID: 10},
		{ID: 2, ProjectID: 10},
	}

	if edges, _ := graph.Build(records, 0.3, graph.MaxEdgesPerNode); len(edges) != 0 {
		t.Errorf("edges = %+v, want none below min weight 0.3", edges)
	}
	if edges, _ := graph.Build(records, 0.25, graph.MaxEdgesPerNode); len(edges) != 1 {
		t.Errorf("edges = %+v, want the project link at weight 0.25", edges)
	}
}

func TestBuildCapsEdgesPerNode(t *testing.T) {
	const records = graph.MaxEdgesPerNode + 5
	inputs := make([]graph.Record, records)
	for i := range inputs {
		inputs[i] = graph.Record{ID: int64(i + 1), ProjectID: 10, Keywords: []string{"sqlite", "fts5"}}
	}

	edges, capped := graph.Build(inputs, 0, graph.MaxEdgesPerNode)
	if !capped {
		t.Error("capped = false, want true when the cap drops links")
	}
	if len(edges) != (records*graph.MaxEdgesPerNode)/2 {
		t.Errorf("edges = %d, want %d", len(edges), (records*graph.MaxEdgesPerNode)/2)
	}

	degree := map[int64]int{}
	for _, edge := range edges {
		degree[edge.FromID]++
		degree[edge.ToID]++
	}
	for id, count := range degree {
		if count > graph.MaxEdgesPerNode {
			t.Errorf("node %d has %d edges, want at most %d", id, count, graph.MaxEdgesPerNode)
		}
	}
}

func assertEdge(t *testing.T, edges []graph.Edge, from, to int64, reason string, weight float64) {
	t.Helper()

	for _, edge := range edges {
		if edge.FromID != from || edge.ToID != to {
			continue
		}
		if edge.Reason != reason {
			t.Errorf("edge %d-%d reason = %q, want %q", from, to, edge.Reason, reason)
		}
		if diff := edge.Weight - weight; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("edge %d-%d weight = %v, want %v", from, to, edge.Weight, weight)
		}
		return
	}
	t.Errorf("edge %d-%d is missing: %+v", from, to, edges)
}
