package store

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/search"
)

// searchFixture is a record-shaped row inserted directly so ranking tests can
// control every FTS column. Card generation (#10) will populate these fields
// through the queue; until then the fixture mirrors the stored shape.
type searchFixture struct {
	ExternalID string
	ProjectKey string
	Source     string
	Title      string
	Summary    string
	Questions  string
	Keywords   string
	Atoms      string
	RawText    string
	UpdatedAt  time.Time
}

func openSearchStore(t *testing.T) (*Store, int64) {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client, _, err := st.CreateClient(context.Background(), "search-fixture", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	return st, client.ID
}

func seedSearchRecord(t *testing.T, st *Store, clientID int64, fixture searchFixture) int64 {
	t.Helper()
	ctx := context.Background()

	if fixture.Source == "" {
		fixture.Source = "opencode"
	}
	if fixture.ProjectKey == "" {
		fixture.ProjectKey = "github.com/SeRGei93/meme"
	}
	if fixture.UpdatedAt.IsZero() {
		fixture.UpdatedAt = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	}

	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO projects (key, name, path, created_at)
		VALUES (?, ?, '', ?)
		ON CONFLICT(key) DO NOTHING`,
		fixture.ProjectKey, fixture.ProjectKey, formatTime(fixture.UpdatedAt),
	); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	var projectID int64
	if err := st.db.QueryRowContext(ctx, "SELECT id FROM projects WHERE key = ?", fixture.ProjectKey).Scan(&projectID); err != nil {
		t.Fatalf("read project: %v", err)
	}

	finalized := formatTime(fixture.UpdatedAt)
	result, err := st.db.ExecContext(ctx, `
		INSERT INTO records (
			client_id, source, external_id, project_id, kind, title, summary,
			questions, keywords, atoms, raw_text, content_hash, revision,
			is_final, finalized_at, started_at, updated_at, indexed_at,
			card_model, card_status, metadata_json
		) VALUES (?, ?, ?, ?, 'session', ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, NULL, 'ok', '{}')`,
		clientID, fixture.Source, fixture.ExternalID, projectID,
		fixture.Title, fixture.Summary, fixture.Questions, fixture.Keywords,
		fixture.Atoms, fixture.RawText, fixture.ExternalID,
		finalized, finalized, finalized, finalized,
	)
	if err != nil {
		t.Fatalf("insert record: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("record id: %v", err)
	}
	return id
}

func seedSearchNoise(t *testing.T, st *Store, clientID int64, count int, now time.Time) {
	t.Helper()
	for i := 0; i < count; i++ {
		seedSearchRecord(t, st, clientID, searchFixture{
			ExternalID: "noise-" + time.Duration(i).String(),
			Title:      "unrelated filler material",
			Summary:    "nothing to see here",
			RawText:    "unrelated filler material",
			UpdatedAt:  now,
		})
	}
}

func TestSearchDecaysScoreWithFreshness(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	seedSearchNoise(t, st, clientID, 10, now)
	same := func(updatedAt time.Time) searchFixture {
		return searchFixture{
			Title:     "needle alpha beta",
			Summary:   "alpha beta gamma",
			Questions: "alpha beta gamma",
			Keywords:  "alpha beta gamma",
			Atoms:     "alpha beta gamma",
			RawText:   "alpha beta gamma",
			UpdatedAt: updatedAt,
		}
	}

	recent := same(now)
	recent.ExternalID = "recent"
	recentID := seedSearchRecord(t, st, clientID, recent)

	old := same(now.Add(-120 * 24 * time.Hour))
	old.ExternalID = "old"
	oldID := seedSearchRecord(t, st, clientID, old)

	future := same(now.Add(30 * 24 * time.Hour))
	future.ExternalID = "future"
	futureID := seedSearchRecord(t, st, clientID, future)

	result, err := st.Search(context.Background(), SearchParams{Query: "needle", Limit: 8, Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len(Items) = %d, want 3", len(result.Items))
	}

	byID := map[int64]float64{}
	for _, hit := range result.Items {
		byID[hit.ID] = hit.Score
	}
	if byID[recentID] <= byID[oldID] {
		t.Errorf("recent score %v <= old score %v, want the fresh record higher", byID[recentID], byID[oldID])
	}
	ratio := byID[oldID] / byID[recentID]
	if ratio < 0.49 || ratio > 0.51 {
		t.Errorf("old/recent score ratio = %v, want 0.5 (120-day half-life)", ratio)
	}
	if byID[futureID] != byID[recentID] {
		t.Errorf("future score = %v, recent score = %v; want equal scores (age clamped at 0)", byID[futureID], byID[recentID])
	}
}

func TestSearchMatchesRussianWordForms(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	seedSearchNoise(t, st, clientID, 10, now)
	recordID := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "russian",
		Title:      "Миграции базы данных",
		Summary:    "таблица projects создаётся первой",
		Keywords:   "sqlite миграция",
		UpdatedAt:  now,
	})
	seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "other",
		Title:      "Настройка CI",
		Summary:    "кэш зависимостей",
		UpdatedAt:  now,
	})

	for _, query := range []string{"миграция", "миграции", "миграцией", "база", "базы", "данные"} {
		t.Run(query, func(t *testing.T) {
			result, err := st.Search(context.Background(), SearchParams{Query: query, Limit: 8, Now: now})
			if err != nil {
				t.Fatalf("Search(%q) error = %v", query, err)
			}
			if len(result.Items) == 0 {
				t.Fatalf("Search(%q) returned no items", query)
			}
			if result.Items[0].ID != recordID {
				t.Errorf("Search(%q) top id = %d, want %d", query, result.Items[0].ID, recordID)
			}
		})
	}
}

func TestSearchHonorsQuotedPhrase(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	dataID := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "data",
		Title:      "База данных SQLite",
		UpdatedAt:  now,
	})
	seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "knowledge",
		Title:      "База знаний проекта",
		UpdatedAt:  now,
	})

	result, err := st.Search(context.Background(), SearchParams{Query: `"база данных"`, Limit: 8, Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != dataID {
		t.Errorf("quoted phrase matches = %+v (total %d), want only record %d", result.Items, result.Total, dataID)
	}
}

func TestSearchRejectsQueryWithoutTerms(t *testing.T) {
	st, _ := openSearchStore(t)

	_, err := st.Search(context.Background(), SearchParams{Query: "  !!!  "})
	if !errors.Is(err, ErrInvalidSearch) {
		t.Errorf("Search() error = %v, want ErrInvalidSearch", err)
	}
}

func TestSearchAppliesProjectSourceAndDateFilters(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	seedSearchNoise(t, st, clientID, 10, now)
	opencodeA := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "opencode-a",
		ProjectKey: "github.com/SeRGei93/meme",
		Source:     "opencode",
		Title:      "needle in project a",
		UpdatedAt:  now.Add(-10 * 24 * time.Hour),
	})
	jarvisA := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "jarvis-a",
		ProjectKey: "github.com/SeRGei93/meme",
		Source:     "jarvis",
		Title:      "needle from jarvis in project a",
		UpdatedAt:  now.Add(-5 * 24 * time.Hour),
	})
	opencodeB := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "opencode-b",
		ProjectKey: "github.com/SeRGei93/other",
		Source:     "opencode",
		Title:      "needle in project b",
		UpdatedAt:  now.Add(-24 * time.Hour),
	})

	since := now.Add(-7 * 24 * time.Hour)
	until := now.Add(-7 * 24 * time.Hour)
	elevenDaysAgo := now.Add(-11 * 24 * time.Hour)

	tests := []struct {
		name   string
		params SearchParams
		want   []int64
	}{
		{
			name:   "project",
			params: SearchParams{ProjectKey: "github.com/SeRGei93/meme"},
			want:   []int64{jarvisA, opencodeA},
		},
		{
			name:   "source",
			params: SearchParams{Source: "jarvis"},
			want:   []int64{jarvisA},
		},
		{
			name:   "since",
			params: SearchParams{Since: &since},
			want:   []int64{opencodeB, jarvisA},
		},
		{
			name:   "until",
			params: SearchParams{Until: &until},
			want:   []int64{opencodeA},
		},
		{
			name:   "combined",
			params: SearchParams{ProjectKey: "github.com/SeRGei93/meme", Source: "opencode", Since: &elevenDaysAgo},
			want:   []int64{opencodeA},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.params.Query = "needle"
			tt.params.Now = now
			result, err := st.Search(context.Background(), tt.params)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if result.Total != len(tt.want) {
				t.Errorf("Total = %d, want %d", result.Total, len(tt.want))
			}
			if len(result.Items) != len(tt.want) {
				t.Fatalf("len(Items) = %d, want %d", len(result.Items), len(tt.want))
			}
			got := make([]int64, len(result.Items))
			for i := range result.Items {
				got[i] = result.Items[i].ID
			}
			want := append([]int64(nil), tt.want...)
			sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
			sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("matching ids = %v, want %v", got, want)
					break
				}
			}
		})
	}
}

func TestSearchReturnsTotalBeforeLimit(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	seedSearchNoise(t, st, clientID, 10, now)
	for i := 0; i < 10; i++ {
		seedSearchRecord(t, st, clientID, searchFixture{
			ExternalID: "match-" + time.Duration(i).String(),
			Title:      "needle alpha",
			UpdatedAt:  now,
		})
	}

	limited, err := st.Search(context.Background(), SearchParams{Query: "needle", Limit: 3, Now: now})
	if err != nil {
		t.Fatalf("Search(limit 3) error = %v", err)
	}
	if len(limited.Items) != 3 {
		t.Errorf("len(Items) = %d, want 3", len(limited.Items))
	}
	if limited.Total != 10 {
		t.Errorf("Total = %d, want 10 before the limit", limited.Total)
	}

	defaulted, err := st.Search(context.Background(), SearchParams{Query: "needle", Now: now})
	if err != nil {
		t.Fatalf("Search(default limit) error = %v", err)
	}
	if len(defaulted.Items) != 8 {
		t.Errorf("len(Items) = %d, want the default 8", len(defaulted.Items))
	}
	if defaulted.Total != 10 {
		t.Errorf("Total = %d, want 10", defaulted.Total)
	}
}

func TestSearchRejectsInvalidParams(t *testing.T) {
	st, _ := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)

	tests := []struct {
		name   string
		params SearchParams
	}{
		{"empty query", SearchParams{Query: " "}},
		{"limit too large", SearchParams{Query: "needle", Limit: search.MaxLimit + 1}},
		{"limit negative", SearchParams{Query: "needle", Limit: -1}},
		{"unknown source", SearchParams{Query: "needle", Source: "unknown"}},
		{"inverted range", SearchParams{Query: "needle", Since: &later, Until: &now}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.Search(context.Background(), tt.params)
			if !errors.Is(err, ErrInvalidSearch) {
				t.Errorf("Search() error = %v, want ErrInvalidSearch", err)
			}
		})
	}
}

// TestSearchRanksByWeightedBM25 pins the relative ranking from
// docs/ARCHITECTURE.md §8.3; the literal weights are asserted in the search
// package's TestRankingConstantsMatchArchitecture.
func TestSearchRanksByWeightedBM25(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	filler := func() searchFixture {
		return searchFixture{
			Title:     "alpha beta gamma",
			Summary:   "alpha beta gamma",
			Questions: "alpha beta gamma",
			Keywords:  "alpha beta gamma",
			Atoms:     "alpha beta gamma",
			RawText:   "alpha beta gamma",
			UpdatedAt: now,
		}
	}
	target := func(column string) searchFixture {
		fixture := filler()
		switch column {
		case "title":
			fixture.Title = "needle alpha beta"
		case "summary":
			fixture.Summary = "needle alpha beta"
		case "questions":
			fixture.Questions = "needle alpha beta"
		case "keywords":
			fixture.Keywords = "needle alpha beta"
		case "atoms":
			fixture.Atoms = "needle alpha beta"
		case "raw_text":
			fixture.RawText = "needle alpha beta"
		}
		return fixture
	}

	seedSearchNoise(t, st, clientID, 10, now)
	ids := map[string]int64{}
	for _, column := range []string{"title", "summary", "questions", "keywords", "atoms", "raw_text"} {
		fixture := target(column)
		fixture.ExternalID = "target-" + column
		ids[column] = seedSearchRecord(t, st, clientID, fixture)
	}

	result, err := st.Search(context.Background(), SearchParams{Query: "needle", Limit: 8, Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.Total != 6 {
		t.Errorf("Total = %d, want 6", result.Total)
	}

	want := []int64{
		ids["title"],
		ids["keywords"],
		ids["questions"],
		ids["summary"],
		ids["atoms"],
		ids["raw_text"],
	}
	if len(result.Items) != len(want) {
		t.Fatalf("len(Items) = %d, want %d", len(result.Items), len(want))
	}
	for i, wantID := range want {
		if result.Items[i].ID != wantID {
			got := make([]int64, len(result.Items))
			for j := range result.Items {
				got[j] = result.Items[j].ID
			}
			t.Fatalf("ranked ids = %v, want %v", got, want)
		}
		if result.Items[i].Score <= 0 {
			t.Errorf("score of %s = %v, want > 0", result.Items[i].Title, result.Items[i].Score)
		}
		if i > 0 && result.Items[i].Score > result.Items[i-1].Score {
			t.Errorf("scores are not descending: %v then %v", result.Items[i-1].Score, result.Items[i].Score)
		}
	}
}

func TestSearchDoesNotOverStemRussianRoots(t *testing.T) {
	st, clientID := openSearchStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	seedSearchNoise(t, st, clientID, 10, now)
	museumID := seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "museum",
		Title:      "История музея",
		UpdatedAt:  now,
	})
	seedSearchRecord(t, st, clientID, searchFixture{
		ExternalID: "music",
		Title:      "Музыка в проекте",
		UpdatedAt:  now,
	})

	for _, query := range []string{"музей", "музея", "музеи"} {
		t.Run(query, func(t *testing.T) {
			result, err := st.Search(context.Background(), SearchParams{Query: query, Limit: 8, Now: now})
			if err != nil {
				t.Fatalf("Search(%q) error = %v", query, err)
			}
			if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != museumID {
				t.Errorf("Search(%q) returned %+v (total %d), want only the museum record %d",
					query, result.Items, result.Total, museumID)
			}
		})
	}
}
