package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/store"
)

type projectBody struct {
	Key            string     `json:"key"`
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	RecordCount    int        `json:"record_count"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

type projectListBody struct {
	Items []projectBody `json:"items"`
}

func seedReadMemory(t *testing.T, env *testEnv, clientID int64, externalID, projectKey string, keywords []string, now time.Time) int64 {
	t.Helper()

	result, err := env.store.SaveMemory(context.Background(), storeMemoryParams(clientID, externalID, projectKey, keywords, now))
	if err != nil {
		t.Fatalf("SaveMemory(%q) error = %v", externalID, err)
	}
	return result.RecordID
}

func storeMemoryParams(clientID int64, externalID, projectKey string, keywords []string, now time.Time) store.MemoryParams {
	return store.MemoryParams{
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
	}
}

type statsBody struct {
	Records struct {
		Total        int            `json:"total"`
		Final        int            `json:"final"`
		Draft        int            `json:"draft"`
		BySource     map[string]int `json:"by_source"`
		ByCardStatus map[string]int `json:"by_card_status"`
	} `json:"records"`
	Projects     int            `json:"projects"`
	Clients      int            `json:"clients"`
	Jobs         map[string]int `json:"jobs"`
	LastIngestAt *time.Time     `json:"last_ingest_at"`
	Database     struct {
		Status        string `json:"status"`
		SchemaVersion int    `json:"schema_version"`
		SizeBytes     int64  `json:"size_bytes"`
	} `json:"database"`
}

type graphBody struct {
	Nodes []struct {
		ID         int64     `json:"id"`
		Title      string    `json:"title"`
		ProjectKey string    `json:"project_key"`
		Source     string    `json:"source"`
		Kind       string    `json:"kind"`
		UpdatedAt  time.Time `json:"updated_at"`
	} `json:"nodes"`
	Edges []struct {
		FromID int64   `json:"from_id"`
		ToID   int64   `json:"to_id"`
		Weight float64 `json:"weight"`
		Reason string  `json:"reason"`
	} `json:"edges"`
	Truncated bool `json:"truncated"`
}

func TestGetGraphReturnsNodesAndEdgesWithUIScope(t *testing.T) {
	env := newTestEnv(t)
	seed, _, err := env.store.CreateClient(context.Background(), "graph-seed", auth.KindJarvis, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	seedReadMemory(t, env, seed.ID, "evt_graph_a", "alpha", []string{"sqlite", "fts5"}, base)
	seedReadMemory(t, env, seed.ID, "evt_graph_b", "alpha", []string{"sqlite", "fts5"}, base.Add(time.Minute))

	token := createClientToken(t, env, "graph-ui", "ui", []string{"ui"})

	rec := env.do(t, http.MethodGet, "/api/v1/graph", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body graphBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode graph: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Nodes) != 2 || len(body.Edges) != 1 {
		t.Fatalf("graph = %d nodes, %d edges, want 2 and 1", len(body.Nodes), len(body.Edges))
	}
	if body.Nodes[0].ProjectKey != "alpha" || body.Nodes[0].Source != "jarvis" || body.Nodes[0].Kind != "memory" {
		t.Errorf("node = %+v, want an alpha jarvis memory", body.Nodes[0])
	}
	edge := body.Edges[0]
	if edge.FromID != body.Nodes[1].ID || edge.ToID != body.Nodes[0].ID {
		t.Errorf("edge = %+v, want links between the two nodes", edge)
	}
	if edge.Reason != "keywords" || edge.Weight != 1 {
		t.Errorf("edge = %+v, want reason keywords and weight 1", edge)
	}
	if body.Truncated {
		t.Error("truncated = true, want false")
	}

	for _, tt := range []struct {
		source string
		nodes  int
	}{
		{"jarvis", 2},
		{"gitlab", 0},
	} {
		filtered := env.do(t, http.MethodGet, "/api/v1/graph?source="+tt.source, token, nil)
		if filtered.Code != http.StatusOK {
			t.Fatalf("source=%s status = %d, want 200 (body %s)", tt.source, filtered.Code, filtered.Body.String())
		}
		var filteredBody graphBody
		if err := json.Unmarshal(filtered.Body.Bytes(), &filteredBody); err != nil {
			t.Fatalf("decode graph source=%s: %v", tt.source, err)
		}
		if len(filteredBody.Nodes) != tt.nodes {
			t.Errorf("source=%s nodes = %d, want %d", tt.source, len(filteredBody.Nodes), tt.nodes)
		}
	}

	scoped := env.do(t, http.MethodGet, "/api/v1/graph?project_key=missing", token, nil)
	if scoped.Code != http.StatusOK {
		t.Fatalf("missing project status = %d, want 200 (body %s)", scoped.Code, scoped.Body.String())
	}
	body = graphBody{}
	if err := json.Unmarshal(scoped.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if len(body.Nodes) != 0 || len(body.Edges) != 0 {
		t.Errorf("missing project graph = %d nodes, %d edges, want empty", len(body.Nodes), len(body.Edges))
	}

	// The graph is a UI surface only; mcp:read and ingest are not enough.
	mcpToken := createClientToken(t, env, "graph-mcp", "jarvis", []string{"mcp:read"})
	if rec := env.do(t, http.MethodGet, "/api/v1/graph", mcpToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("mcp:read graph status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	ingestToken := createClientToken(t, env, "graph-ingest", "jarvis", []string{"ingest"})
	if rec := env.do(t, http.MethodGet, "/api/v1/graph", ingestToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("ingest graph status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodGet, "/api/v1/graph", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous graph status = %d, want 401", rec.Code)
	}

	for _, query := range []string{"?limit=", "?limit=0", "?limit=501", "?min_weight=", "?min_weight=-0.1", "?min_weight=1.1", "?min_weight=NaN", "?project_key="} {
		if rec := env.do(t, http.MethodGet, "/api/v1/graph"+query, token, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("graph%s status = %d, want 400 (body %s)", query, rec.Code, rec.Body.String())
		}
	}
}

func TestReadEndpointsEnforceScopes(t *testing.T) {
	env := newTestEnv(t)

	mcpToken := createClientToken(t, env, "read-mcp", "jarvis", []string{"mcp:read"})
	ingestToken := createClientToken(t, env, "read-ingest", "jarvis", []string{"ingest"})

	tests := []struct {
		name       string
		path       string
		mcpStatus  int
		ingestCode int
	}{
		{name: "projects", path: "/api/v1/projects", mcpStatus: http.StatusOK, ingestCode: http.StatusForbidden},
		{name: "stats", path: "/api/v1/stats", mcpStatus: http.StatusOK, ingestCode: http.StatusForbidden},
		{name: "graph", path: "/api/v1/graph", mcpStatus: http.StatusForbidden, ingestCode: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if rec := env.do(t, http.MethodGet, test.path, mcpToken, nil); rec.Code != test.mcpStatus {
				t.Errorf("mcp:read status = %d, want %d (body %s)", rec.Code, test.mcpStatus, rec.Body.String())
			}
			if rec := env.do(t, http.MethodGet, test.path, ingestToken, nil); rec.Code != test.ingestCode {
				t.Errorf("ingest-only status = %d, want %d (body %s)", rec.Code, test.ingestCode, rec.Body.String())
			}
			if rec := env.do(t, http.MethodGet, test.path, "", nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("anonymous status = %d, want 401", rec.Code)
			}
			if rec := env.do(t, http.MethodGet, test.path, testAdminToken, nil); rec.Code != http.StatusOK {
				t.Errorf("bootstrap status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGetStatsReturnsAggregates(t *testing.T) {
	env := newTestEnv(t)

	// The bootstrap token owns every scope, so the empty snapshot is readable
	// before any client exists.
	rec := env.do(t, http.MethodGet, "/api/v1/stats", testAdminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty stats status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var empty statsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode stats: %v (body %s)", err, rec.Body.String())
	}
	if empty.Records.Total != 0 || empty.Projects != 0 || empty.Clients != 0 {
		t.Errorf("empty stats = %+v, want zero records/projects/clients", empty)
	}
	if empty.LastIngestAt != nil {
		t.Errorf("empty last_ingest_at = %v, want null", empty.LastIngestAt)
	}
	if len(empty.Records.BySource) != 0 {
		t.Errorf("empty by_source = %v, want an empty map", empty.Records.BySource)
	}
	for _, key := range []string{"heuristic", "queued", "ok", "failed"} {
		if _, ok := empty.Records.ByCardStatus[key]; !ok {
			t.Errorf("by_card_status is missing %q: %v", key, empty.Records.ByCardStatus)
		}
	}
	if empty.Database.Status != "ok" || empty.Database.SchemaVersion != 4 || empty.Database.SizeBytes <= 0 {
		t.Errorf("database = %+v, want ok, schema 4, positive size", empty.Database)
	}

	seed, _, err := env.store.CreateClient(context.Background(), "stats-seed", auth.KindJarvis, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	seedReadMemory(t, env, seed.ID, "evt_stats_1", "second-brain", []string{"sqlite"}, base)
	seedReadMemory(t, env, seed.ID, "evt_stats_2", "second-brain", []string{"fts5"}, base.Add(time.Hour))

	token := createClientToken(t, env, "stats-ui", "ui", []string{"ui"})
	rec = env.do(t, http.MethodGet, "/api/v1/stats", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body statsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode stats: %v (body %s)", err, rec.Body.String())
	}
	if body.Records.Total != 2 || body.Records.Final != 2 || body.Records.Draft != 0 {
		t.Errorf("records = %+v, want total 2, final 2, draft 0", body.Records)
	}
	if body.Records.BySource["jarvis"] != 2 || len(body.Records.BySource) != 1 {
		t.Errorf("by_source = %v, want only jarvis 2", body.Records.BySource)
	}
	if body.Records.ByCardStatus["ok"] != 2 {
		t.Errorf("by_card_status = %v, want ok 2", body.Records.ByCardStatus)
	}
	if body.Projects != 1 || body.Clients != 2 {
		t.Errorf("projects = %d, clients = %d, want 1 and 2 active clients", body.Projects, body.Clients)
	}
	if body.LastIngestAt == nil || !body.LastIngestAt.Equal(base.Add(time.Hour)) {
		t.Errorf("last_ingest_at = %v, want %v", body.LastIngestAt, base.Add(time.Hour))
	}
}

func TestListProjectsReturnsItemsAndHonorsLimit(t *testing.T) {
	env := newTestEnv(t)
	seed, _, err := env.store.CreateClient(context.Background(), "projects-seed", auth.KindJarvis, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	seedReadMemory(t, env, seed.ID, "evt_one", "second-brain", []string{"sqlite"}, base)
	seedReadMemory(t, env, seed.ID, "evt_two", "second-brain", []string{"fts5"}, base.Add(time.Hour))
	seedReadMemory(t, env, seed.ID, "evt_three", "jarvis", nil, base.Add(2*time.Hour))

	token := createClientToken(t, env, "projects-ui", "ui", []string{"ui"})

	rec := env.do(t, http.MethodGet, "/api/v1/projects", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var body projectListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode projects: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2: %+v", len(body.Items), body.Items)
	}
	if body.Items[0].Key != "jarvis" {
		t.Errorf("first project = %q, want jarvis (most recent activity)", body.Items[0].Key)
	}
	if body.Items[1].Key != "second-brain" || body.Items[1].RecordCount != 2 {
		t.Errorf("second project = %+v, want second-brain with 2 records", body.Items[1])
	}

	rec = env.do(t, http.MethodGet, "/api/v1/projects?limit=1", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit=1 status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body = projectListBody{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Key != "jarvis" {
		t.Errorf("limit=1 items = %+v, want only jarvis", body.Items)
	}

	rec = env.do(t, http.MethodGet, "/api/v1/projects?limit=501", token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("limit=501 status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	rec = env.do(t, http.MethodGet, "/api/v1/projects?limit=", token, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty limit status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
}
