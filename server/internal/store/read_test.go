package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/store"
)

func readClient(t *testing.T, st *store.Store, name string, kind auth.Kind) int64 {
	t.Helper()

	client, _, err := st.CreateClient(context.Background(), name, kind, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient(%q) error = %v", name, err)
	}
	return client.ID
}

func saveReadMemory(t *testing.T, st *store.Store, clientID int64, externalID, projectKey string, keywords []string, now time.Time) int64 {
	t.Helper()

	result, err := st.SaveMemory(context.Background(), store.MemoryParams{
		ClientID:       clientID,
		Source:         "jarvis",
		IdempotencyKey: externalID + "-key",
		Request: memory.Request{
			ExternalID: externalID,
			ProjectKey: projectKey,
			Title:      externalID,
			Summary:    "Summary for " + externalID,
			Keywords:   keywords,
			RawText:    "Raw text for " + externalID,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("SaveMemory(%q) error = %v", externalID, err)
	}
	return result.RecordID
}

func saveReadMemoryTextOnly(t *testing.T, st *store.Store, clientID int64, externalID, projectKey string, now time.Time) int64 {
	t.Helper()

	result, err := st.SaveMemory(context.Background(), store.MemoryParams{
		ClientID:       clientID,
		Source:         "jarvis",
		IdempotencyKey: externalID + "-key",
		Request: memory.Request{
			ExternalID: externalID,
			ProjectKey: projectKey,
			RawText:    "Raw text for " + externalID,
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("SaveMemory(%q) error = %v", externalID, err)
	}
	return result.RecordID
}

func ingestReadSession(t *testing.T, st *store.Store, clientID int64, externalID, projectKey, projectPath string, updatedAt time.Time, final bool) int64 {
	t.Helper()

	result, err := st.IngestSession(context.Background(), store.IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: externalID + "-key",
		Bundle: ingest.Bundle{
			BundleID:    externalID + "-bundle",
			ExternalID:  externalID,
			ProjectKey:  projectKey,
			ProjectPath: projectPath,
			Title:       externalID,
			Final:       final,
			StartedAt:   updatedAt.Add(-time.Hour),
			UpdatedAt:   updatedAt,
			Messages: []ingest.Message{
				{ID: "msg_1", Role: ingest.RoleUser, Text: "Question", At: updatedAt.Add(-time.Minute)},
				{ID: "msg_2", Role: ingest.RoleAssistant, Text: "Answer", At: updatedAt},
			},
			Metadata: ingest.Metadata{"agent": "build"},
		},
		Now: updatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("IngestSession(%q) error = %v", externalID, err)
	}
	return result.RecordID
}

func TestGraphLinksRecordsBySharedProjectAndKeywords(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	clientID := readClient(t, st, "graph-reader", auth.KindJarvis)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	a := saveReadMemory(t, st, clientID, "evt_a", "alpha", []string{"sqlite", "fts5", "search"}, base)
	b := saveReadMemory(t, st, clientID, "evt_b", "alpha", []string{"sqlite", "fts5"}, base.Add(time.Minute))
	c := saveReadMemory(t, st, clientID, "evt_c", "beta", []string{"sqlite"}, base.Add(2*time.Minute))
	d := saveReadMemory(t, st, clientID, "evt_d", "beta", []string{"redis"}, base.Add(3*time.Minute))

	graph, err := st.Graph(ctx, store.GraphParams{})
	if err != nil {
		t.Fatalf("Graph() error = %v", err)
	}

	if len(graph.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4: %+v", len(graph.Nodes), graph.Nodes)
	}
	if graph.Nodes[0].ID != d || graph.Nodes[3].ID != a {
		t.Errorf("node order = %v, want newest first (d, c, b, a)", nodeIDs(graph.Nodes))
	}
	for _, node := range graph.Nodes {
		if node.Title == "" || node.Source != "jarvis" || node.Kind != "memory" {
			t.Errorf("node = %+v, want a jarvis memory with a title", node)
		}
	}

	if len(graph.Edges) != 4 {
		t.Fatalf("edges = %d, want 4: %+v", len(graph.Edges), graph.Edges)
	}
	assertEdge(t, graph, a, b, "keywords", 0.25+0.75*2.0/3.0)
	assertEdge(t, graph, a, c, "keywords", 0.25)
	assertEdge(t, graph, b, c, "keywords", 0.75*0.5)
	assertEdge(t, graph, c, d, "project", 0.25)
	if graph.Truncated {
		t.Error("truncated = true, want false for a complete graph")
	}

	filtered, err := st.Graph(ctx, store.GraphParams{MinWeight: 0.7})
	if err != nil {
		t.Fatalf("Graph(min_weight 0.7) error = %v", err)
	}
	if len(filtered.Edges) != 1 {
		t.Fatalf("min_weight 0.7 edges = %+v, want only the strongest keyword edge", filtered.Edges)
	}
	assertEdge(t, filtered, a, b, "keywords", 0.25+0.75*2.0/3.0)

	scoped, err := st.Graph(ctx, store.GraphParams{ProjectKey: "alpha"})
	if err != nil {
		t.Fatalf("Graph(project alpha) error = %v", err)
	}
	if len(scoped.Nodes) != 2 || len(scoped.Edges) != 1 {
		t.Errorf("project alpha graph = %d nodes, %d edges, want 2 and 1", len(scoped.Nodes), len(scoped.Edges))
	}
	assertEdge(t, scoped, a, b, "keywords", 0.25+0.75*2.0/3.0)

	limited, err := st.Graph(ctx, store.GraphParams{Limit: 2})
	if err != nil {
		t.Fatalf("Graph(limit 2) error = %v", err)
	}
	if len(limited.Nodes) != 2 || !limited.Truncated {
		t.Errorf("limit 2 graph = %d nodes, truncated %v, want 2 and true", len(limited.Nodes), limited.Truncated)
	}
}

func TestGraphCapsEdgesPerNode(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	clientID := readClient(t, st, "graph-cap", auth.KindJarvis)
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	const records = store.MaxGraphEdgesPerNode + 5
	ids := make([]int64, records)
	for i := range ids {
		ids[i] = saveReadMemory(t, st, clientID, "evt_cap_"+string(rune('a'+i)), "alpha",
			[]string{"sqlite", "fts5"}, base.Add(time.Duration(i)*time.Minute))
	}

	graph, err := st.Graph(ctx, store.GraphParams{})
	if err != nil {
		t.Fatalf("Graph() error = %v", err)
	}
	if len(graph.Nodes) != records {
		t.Fatalf("nodes = %d, want %d", len(graph.Nodes), records)
	}
	if !graph.Truncated {
		t.Error("truncated = false, want true when the per-node edge cap drops links")
	}

	degree := map[int64]int{}
	for _, edge := range graph.Edges {
		degree[edge.FromID]++
		degree[edge.ToID]++
	}
	for id, count := range degree {
		if count > store.MaxGraphEdgesPerNode {
			t.Errorf("node %d has %d edges, want at most %d", id, count, store.MaxGraphEdgesPerNode)
		}
	}
	if len(graph.Edges) != (records*store.MaxGraphEdgesPerNode)/2 {
		t.Errorf("edges = %d, want %d", len(graph.Edges), (records*store.MaxGraphEdgesPerNode)/2)
	}
}

func assertEdge(t *testing.T, graph store.Graph, first, second int64, reason string, weight float64) {
	t.Helper()

	from, to := first, second
	if from > to {
		from, to = to, from
	}
	for _, edge := range graph.Edges {
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
	t.Errorf("edge %d-%d is missing; edges = %+v", from, to, graph.Edges)
}

func nodeIDs(nodes []store.GraphNode) []int64 {
	ids := make([]int64, len(nodes))
	for i, node := range nodes {
		ids[i] = node.ID
	}
	return ids
}

func TestStatsAggregatesRecordsProjectsClientsAndJobs(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

	opencodeID := readClient(t, st, "stats-opencode", auth.KindOpenCode)
	jarvisID := readClient(t, st, "stats-jarvis", auth.KindJarvis)
	revoked, _, err := st.CreateClient(ctx, "stats-revoked", auth.KindJarvis, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient(revoked) error = %v", err)
	}
	if err := st.RevokeClient(ctx, revoked.ID); err != nil {
		t.Fatalf("RevokeClient() error = %v", err)
	}

	// Two final OpenCode sessions drive the job lifecycle: one done, one failed.
	ingestReadSession(t, st, opencodeID, "ses_done", "alpha", "/work/alpha", base.Add(time.Hour), true)
	doneJob, ok, err := st.ClaimNextJob(ctx, base.Add(90*time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimNextJob() = %v, %v, want a job", ok, err)
	}
	if err := st.CompleteJob(ctx, doneJob.ID, base.Add(100*time.Minute)); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	ingestReadSession(t, st, opencodeID, "ses_failed", "alpha", "", base.Add(2*time.Hour), true)
	failedJob, ok, err := st.ClaimNextJob(ctx, base.Add(150*time.Minute))
	if err != nil || !ok {
		t.Fatalf("ClaimNextJob() = %v, %v, want a job", ok, err)
	}
	if err := st.FailJob(ctx, failedJob.ID, base.Add(160*time.Minute), "llm unavailable"); err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}

	// A live capture stays draft and never queues a job.
	ingestReadSession(t, st, opencodeID, "ses_live", "alpha", "", base.Add(30*time.Minute), false)

	// Memories cover the two remaining card states and the queued job.
	saveReadMemory(t, st, jarvisID, "evt_ok_1", "second-brain", nil, base.Add(10*time.Minute))
	saveReadMemory(t, st, jarvisID, "evt_ok_2", "second-brain", nil, base.Add(20*time.Minute))
	saveReadMemoryTextOnly(t, st, jarvisID, "evt_queued", "jarvis", base.Add(3*time.Hour))

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}

	if stats.Records.Total != 6 || stats.Records.Final != 5 || stats.Records.Draft != 1 {
		t.Errorf("records = %+v, want total 6, final 5, draft 1", stats.Records)
	}
	if stats.Records.BySource["opencode"] != 3 || stats.Records.BySource["jarvis"] != 3 {
		t.Errorf("by_source = %+v, want opencode 3, jarvis 3", stats.Records.BySource)
	}
	wantCards := store.CardStatusCounts{Heuristic: 1, Queued: 2, OK: 2, Failed: 1}
	if stats.Records.ByCardStatus != wantCards {
		t.Errorf("by_card_status = %+v, want %+v", stats.Records.ByCardStatus, wantCards)
	}
	wantJobs := store.JobCounts{Queued: 1, Running: 0, Done: 1, Failed: 1}
	if stats.Jobs != wantJobs {
		t.Errorf("jobs = %+v, want %+v", stats.Jobs, wantJobs)
	}
	if stats.Projects != 3 {
		t.Errorf("projects = %d, want 3", stats.Projects)
	}
	if stats.Clients != 2 {
		t.Errorf("clients = %d, want 2 active clients", stats.Clients)
	}
	if stats.LastIngestAt == nil || !stats.LastIngestAt.Equal(base.Add(3*time.Hour)) {
		t.Errorf("last_ingest_at = %v, want %v", stats.LastIngestAt, base.Add(3*time.Hour))
	}
	if stats.Database.Status != "ok" || stats.Database.SchemaVersion != 4 || stats.Database.SizeBytes <= 0 {
		t.Errorf("database = %+v, want ok, schema 3, positive size", stats.Database)
	}
}

func TestListProjectsReturnsRecordCountsAndLastActivity(t *testing.T) {
	st, _ := openTestStore(t)
	clientID := readClient(t, st, "project-reader", auth.KindJarvis)
	ctx := context.Background()

	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	const memeKey = "github.com/SeRGei93/meme"

	saveReadMemory(t, st, clientID, "evt_meme_1", memeKey, nil, base)
	ingestReadSession(t, st, clientID, "ses_meme_2", memeKey,
		"/Users/serg/PhpstormProjects/meme", base.Add(2*time.Hour), true)
	saveReadMemory(t, st, clientID, "evt_brain_1", "second-brain", nil, base.Add(3*time.Hour))

	projects, err := st.ListProjects(ctx, store.ListProjectsParams{Limit: 100})
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("ListProjects() returned %d projects, want 2: %+v", len(projects), projects)
	}

	newest := projects[0]
	if newest.Key != "second-brain" {
		t.Errorf("first project key = %q, want second-brain (most recent activity)", newest.Key)
	}
	if newest.RecordCount != 1 {
		t.Errorf("second-brain record_count = %d, want 1", newest.RecordCount)
	}
	if newest.LastActivityAt == nil || !newest.LastActivityAt.Equal(base.Add(3*time.Hour)) {
		t.Errorf("second-brain last_activity_at = %v, want %v", newest.LastActivityAt, base.Add(3*time.Hour))
	}

	meme := projects[1]
	if meme.Key != memeKey {
		t.Errorf("second project key = %q, want %q", meme.Key, memeKey)
	}
	if meme.Name != "meme" {
		t.Errorf("meme name = %q, want meme", meme.Name)
	}
	if meme.Path != "/Users/serg/PhpstormProjects/meme" {
		t.Errorf("meme path = %q, want the ingest project path", meme.Path)
	}
	if meme.RecordCount != 2 {
		t.Errorf("meme record_count = %d, want 2", meme.RecordCount)
	}
	if meme.LastActivityAt == nil || !meme.LastActivityAt.Equal(base.Add(2*time.Hour)) {
		t.Errorf("meme last_activity_at = %v, want %v", meme.LastActivityAt, base.Add(2*time.Hour))
	}
	if !meme.CreatedAt.Equal(base) {
		t.Errorf("meme created_at = %v, want %v (first record)", meme.CreatedAt, base)
	}

	limited, err := st.ListProjects(ctx, store.ListProjectsParams{Limit: 1})
	if err != nil {
		t.Fatalf("ListProjects(limit 1) error = %v", err)
	}
	if len(limited) != 1 || limited[0].Key != "second-brain" {
		t.Errorf("ListProjects(limit 1) = %+v, want only second-brain", limited)
	}
}
