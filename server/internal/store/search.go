package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/search"
)

// ErrInvalidSearch is returned when a search request violates the v1 contract
// (empty query, bad limit, unknown source or inverted date range).
var ErrInvalidSearch = errors.New("invalid search request")

// searchFrom is shared by the hit page and the total count so a new filter or
// join cannot silently apply to only one of them.
const searchFrom = `
	FROM records_fts
	JOIN records r ON r.id = records_fts.rowid
	LEFT JOIN projects p ON p.id = r.project_id
	WHERE records_fts MATCH ?`

// SearchParams is one ranked full-text query. Zero values mean "not set":
// Limit defaults to 8, Source matches every source and Since/Until are open.
type SearchParams struct {
	Query      string
	Limit      int
	ProjectKey string
	Source     string
	Since      *time.Time
	Until      *time.Time
	Now        time.Time
}

// SearchHit is a card-shaped search result. It deliberately excludes raw_text:
// GET /api/v1/records/{id} is the only way to fetch the full record.
type SearchHit struct {
	ID          int64
	Source      string
	Kind        string
	ProjectKey  string
	Title       string
	Summary     string
	Questions   []string
	Keywords    []string
	Atoms       []string
	IsFinal     bool
	StartedAt   *time.Time
	UpdatedAt   time.Time
	FinalizedAt *time.Time
	CardStatus  string
	CardModel   *string
	Score       float64
}

// SearchResult carries the page of hits and the total number of matches before
// Limit is applied.
type SearchResult struct {
	Items []SearchHit
	Total int
}

// Search runs an FTS5 MATCH query and ranks matches by weighted BM25 decayed by
// freshness: score = -bm25(records_fts, 6, 4, 4, 5, 3, 1) * 0.5^(age_days/120).
// Age is clamped at zero so a future-dated client capture cannot outrank real
// recent knowledge.
func (s *Store) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	match, err := search.MatchQuery(params.Query)
	if err != nil {
		return SearchResult{}, fmt.Errorf("%w: %w", ErrInvalidSearch, err)
	}

	limit := params.Limit
	if limit == 0 {
		limit = search.DefaultLimit
	}
	if limit < 1 || limit > search.MaxLimit {
		return SearchResult{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidSearch, search.MaxLimit)
	}
	if params.Source != "" && utf8.RuneCountInString(params.Source) > auth.MaxSourceQueryLen {
		return SearchResult{}, fmt.Errorf("%w: source must be at most %d characters", ErrInvalidSearch, auth.MaxSourceQueryLen)
	}
	if params.Since != nil && params.Until != nil && params.Since.After(*params.Until) {
		return SearchResult{}, fmt.Errorf("%w: since must not be after until", ErrInvalidSearch)
	}

	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	filters, filterArgs := searchFilterClause(params)

	hitArgs := append([]any{formatTime(now), match}, filterArgs...)
	hitArgs = append(hitArgs, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			r.id, r.source, r.kind, COALESCE(p.key, ''), r.title, r.summary,
			r.questions, r.keywords, r.atoms, r.is_final, r.started_at,
			r.updated_at, r.finalized_at, r.card_status, r.card_model,
			-bm25(records_fts, `+search.BM25Weights+`) *
				pow(0.5, MAX(0.0, (julianday(?) - julianday(r.updated_at)) / `+
		fmt.Sprintf("%.1f", search.FreshnessHalfLifeDays)+`)) AS score`+
		searchFrom+filters+`
		ORDER BY score DESC, r.updated_at DESC, r.id DESC
		LIMIT ?`,
		hitArgs...,
	)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search records: %w", err)
	}
	defer rows.Close()

	result := SearchResult{Items: []SearchHit{}}
	for rows.Next() {
		hit, err := scanSearchHit(rows)
		if err != nil {
			return SearchResult{}, fmt.Errorf("scan search hit: %w", err)
		}
		result.Items = append(result.Items, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, fmt.Errorf("search records: %w", err)
	}

	countArgs := append([]any{match}, filterArgs...)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+searchFrom+filters,
		countArgs...,
	).Scan(&result.Total); err != nil {
		return SearchResult{}, fmt.Errorf("count search results: %w", err)
	}

	return result, nil
}

func searchFilterClause(params SearchParams) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if params.ProjectKey != "" {
		clauses = append(clauses, "p.key = ?")
		args = append(args, params.ProjectKey)
	}
	if params.Source != "" {
		clauses = append(clauses, "r.source = ?")
		args = append(args, params.Source)
	}
	if params.Since != nil {
		clauses = append(clauses, "julianday(r.updated_at) >= julianday(?)")
		args = append(args, formatTime(*params.Since))
	}
	if params.Until != nil {
		clauses = append(clauses, "julianday(r.updated_at) <= julianday(?)")
		args = append(args, formatTime(*params.Until))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}

func scanSearchHit(scanner rowScanner) (SearchHit, error) {
	var (
		hit         SearchHit
		questions   string
		keywords    string
		atoms       string
		isFinal     int
		startedAt   sql.NullString
		updatedAt   string
		finalizedAt sql.NullString
		cardModel   sql.NullString
	)
	if err := scanner.Scan(
		&hit.ID, &hit.Source, &hit.Kind, &hit.ProjectKey, &hit.Title, &hit.Summary,
		&questions, &keywords, &atoms, &isFinal, &startedAt,
		&updatedAt, &finalizedAt, &hit.CardStatus, &cardModel, &hit.Score,
	); err != nil {
		return SearchHit{}, err
	}

	hit.Questions = splitList(questions)
	hit.Keywords = splitList(keywords)
	hit.Atoms = splitList(atoms)
	hit.IsFinal = isFinal != 0

	var err error
	if hit.StartedAt, err = parseNullableTime(startedAt); err != nil {
		return SearchHit{}, err
	}
	if hit.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return SearchHit{}, err
	}
	if hit.FinalizedAt, err = parseNullableTime(finalizedAt); err != nil {
		return SearchHit{}, err
	}
	if cardModel.Valid {
		hit.CardModel = &cardModel.String
	}
	return hit, nil
}
