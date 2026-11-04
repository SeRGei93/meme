package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type searchBody struct {
	Items []searchHitBody `json:"items"`
	Total int             `json:"total"`
	Query string          `json:"query"`
	Limit int             `json:"limit"`
}

type searchHitBody struct {
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

func doSearch(t *testing.T, env *testEnv, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	return env.do(t, http.MethodGet, "/api/v1/search?"+query, token, nil)
}

func decodeSearch(t *testing.T, rec *httptest.ResponseRecorder) searchBody {
	t.Helper()
	var body searchBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func TestSearchRequiresReadScope(t *testing.T) {
	env := newTestEnv(t)
	ingestToken := createClientToken(t, env, "capture", "opencode", []string{"ingest"})
	mcpToken := createClientToken(t, env, "mcp-reader", "opencode", []string{"mcp:read"})

	t.Run("missing token", func(t *testing.T) {
		rec := doSearch(t, env, "", "q=needle")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("ingest only", func(t *testing.T) {
		rec := doSearch(t, env, ingestToken, "q=needle")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
		}
		envelope := decodeError(t, rec)
		if envelope.Error.Code != "forbidden" {
			t.Errorf("code = %q, want forbidden", envelope.Error.Code)
		}
	})

	t.Run("mcp read", func(t *testing.T) {
		rec := doSearch(t, env, mcpToken, "q=needle")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		body := decodeSearch(t, rec)
		if body.Items == nil {
			t.Error("items = null, want an empty array")
		}
		if body.Total != 0 {
			t.Errorf("total = %d, want 0", body.Total)
		}
	})
}

func TestSearchValidatesParameters(t *testing.T) {
	env := newTestEnv(t)
	token := createClientToken(t, env, "ui-reader", "ui", []string{"ui"})

	longQuery := strings.Repeat("я", 501)

	tests := []struct {
		name  string
		query string
		field string
	}{
		{"missing q", "", "q"},
		{"blank q", "q=%20%20", "q"},
		{"no searchable terms", "q=%21%21%21", ""},
		{"query too long", "q=" + url.QueryEscape(longQuery), "q"},
		{"limit zero", "q=needle&limit=0", "limit"},
		{"limit too large", "q=needle&limit=51", "limit"},
		{"limit not a number", "q=needle&limit=abc", "limit"},
		{"unknown source", "q=needle&source=gitlab", "source"},
		{"empty source", "q=needle&source=", "source"},
		{"empty project key", "q=needle&project_key=", "project_key"},
		{"invalid since", "q=needle&since=yesterday", "since"},
		{"invalid until", "q=needle&until=2026-11-04", "until"},
		{"inverted range", "q=needle&since=2026-11-04T13:00:00Z&until=2026-11-04T12:00:00Z", "since"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doSearch(t, env, token, tt.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != "invalid_request" {
				t.Errorf("code = %q, want invalid_request", envelope.Error.Code)
			}
			if tt.field != "" && envelope.Error.Details["field"] != tt.field {
				t.Errorf("details.field = %v, want %q", envelope.Error.Details["field"], tt.field)
			}
		})
	}
}

func TestSearchFiltersByProjectAndDate(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	first := validBundle()
	if rec := doIngest(t, env, ingestToken, first, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	other := cloneBundle(t, validBundle())
	other["bundle_id"] = "7a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	other["external_id"] = "ses_OTHER"
	other["project_key"] = "github.com/SeRGei93/other"
	other["project_path"] = "/Users/serg/PhpstormProjects/other"
	other["title"] = "SQLite backup script"
	other["messages"] = []any{
		map[string]any{
			"id":   "msg_1",
			"role": "user",
			"text": "How do we back up SQLite?",
			"at":   "2026-11-03T12:00:00Z",
		},
	}
	if rec := doIngest(t, env, ingestToken, other, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("second ingest: status = %d (body %s)", rec.Code, rec.Body.String())
	}

	all := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite"))
	if all.Total != 2 {
		t.Errorf("unfiltered total = %d, want 2", all.Total)
	}

	project := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&project_key=github.com%2FSeRGei93%2Fmeme"))
	if project.Total != 1 || len(project.Items) != 1 {
		t.Fatalf("project filter returned %d items (total %d), want 1", len(project.Items), project.Total)
	}
	if project.Items[0].ProjectKey != "github.com/SeRGei93/meme" {
		t.Errorf("project filter hit = %q, want the meme project", project.Items[0].ProjectKey)
	}

	opencode := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&source=opencode"))
	if opencode.Total != 2 {
		t.Errorf("source=opencode total = %d, want 2", opencode.Total)
	}
	jarvis := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&source=jarvis"))
	if jarvis.Total != 0 {
		t.Errorf("source=jarvis total = %d, want 0", jarvis.Total)
	}

	since := url.QueryEscape("2026-11-03T00:00:00Z")
	recent := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&since="+since))
	if recent.Total != 2 {
		t.Errorf("since 2026-11-03 total = %d, want 2", recent.Total)
	}

	until := url.QueryEscape("2026-11-03T13:00:00Z")
	before := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&until="+until))
	if before.Total != 2 {
		t.Errorf("until 2026-11-03T13:00 total = %d, want 2", before.Total)
	}

	future := url.QueryEscape("2026-11-04T00:00:00Z")
	none := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&since="+future))
	if none.Total != 0 || len(none.Items) != 0 {
		t.Errorf("since 2026-11-04 returned %d items (total %d), want 0", len(none.Items), none.Total)
	}

	past := url.QueryEscape("2026-11-02T00:00:00Z")
	stale := decodeSearch(t, doSearch(t, env, readToken, "q=sqlite&until="+past))
	if stale.Total != 0 || len(stale.Items) != 0 {
		t.Errorf("until 2026-11-02 returned %d items (total %d), want 0", len(stale.Items), stale.Total)
	}
}

func TestSearchReturnsTopEightByDefault(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	for i := 0; i < 10; i++ {
		bundle := validBundle()
		bundle["bundle_id"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		bundle["external_id"] = fmt.Sprintf("ses_%d", i)
		bundle["title"] = fmt.Sprintf("needle session %d", i)
		if rec := doIngest(t, env, ingestToken, bundle, ""); rec.Code != http.StatusAccepted {
			t.Fatalf("ingest %d: status = %d (body %s)", i, rec.Code, rec.Body.String())
		}
	}

	rec := doSearch(t, env, readToken, "q=needle")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeSearch(t, rec)
	if len(body.Items) != 8 {
		t.Errorf("len(items) = %d, want the default top-8", len(body.Items))
	}
	if body.Total != 10 {
		t.Errorf("total = %d, want 10", body.Total)
	}
	if body.Limit != 8 {
		t.Errorf("limit = %d, want 8", body.Limit)
	}
}

func TestSearchReturnsRankedCardsWithoutRawText(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	ingested := doIngest(t, env, ingestToken, validBundle(), "")
	if ingested.Code != http.StatusAccepted {
		t.Fatalf("ingest: status = %d, want 202 (body %s)", ingested.Code, ingested.Body.String())
	}
	recordID := decodeIngestResponse(t, ingested).RecordID

	rec := doSearch(t, env, readToken, "q=migration")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw search response: %v", err)
	}
	if len(raw.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(raw.Items))
	}
	if _, ok := raw.Items[0]["raw_text"]; ok {
		t.Error("search hit contains raw_text, want cards only")
	}

	body := decodeSearch(t, rec)
	if body.Query != "migration" {
		t.Errorf("query = %q, want migration", body.Query)
	}
	if body.Limit != 8 {
		t.Errorf("limit = %d, want the default 8", body.Limit)
	}
	if body.Total != 1 {
		t.Errorf("total = %d, want 1", body.Total)
	}
	hit := body.Items[0]
	if hit.ID != recordID {
		t.Errorf("hit.id = %d, want %d", hit.ID, recordID)
	}
	if hit.Source != "opencode" || hit.Kind != "session" {
		t.Errorf("hit source/kind = %q/%q, want opencode/session", hit.Source, hit.Kind)
	}
	if hit.Title != "Fix SQLite migration ordering" {
		t.Errorf("hit.title = %q, want the ingested title", hit.Title)
	}
	if hit.ProjectKey != "github.com/SeRGei93/meme" {
		t.Errorf("hit.project_key = %q", hit.ProjectKey)
	}
	if hit.Score <= 0 {
		t.Errorf("hit.score = %v, want > 0", hit.Score)
	}
	if hit.Questions == nil || hit.Keywords == nil || hit.Atoms == nil {
		t.Errorf("hit card lists = (%v, %v, %v), want empty arrays not null", hit.Questions, hit.Keywords, hit.Atoms)
	}
}
