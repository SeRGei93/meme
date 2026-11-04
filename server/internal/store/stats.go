package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SourceCounts is the per-source record breakdown required by GET
// /api/v1/stats. Both v1 sources are always present, even when zero.
type SourceCounts struct {
	OpenCode int
	Jarvis   int
}

// CardStatusCounts is the per-card-status record breakdown.
type CardStatusCounts struct {
	Heuristic int
	Queued    int
	OK        int
	Failed    int
}

// JobCounts is the queue state grouped by job status.
type JobCounts struct {
	Queued  int
	Running int
	Done    int
	Failed  int
}

// RecordStats is the record half of GET /api/v1/stats.
type RecordStats struct {
	Total        int
	Final        int
	Draft        int
	BySource     SourceCounts
	ByCardStatus CardStatusCounts
}

// DatabaseStats is the database block of GET /api/v1/stats.
type DatabaseStats struct {
	Status        string
	SchemaVersion int
	SizeBytes     int64
}

// Stats is the aggregate snapshot served at GET /api/v1/stats.
type Stats struct {
	Records      RecordStats
	Projects     int
	Clients      int
	Jobs         JobCounts
	LastIngestAt *time.Time
	Database     DatabaseStats
}

// Stats counts records, projects, active clients and the queue in one
// consistent snapshot. A failed sub-query fails the whole call instead of
// returning a partially filled response.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(is_final), 0) FROM records`,
	).Scan(&stats.Records.Total, &stats.Records.Final); err != nil {
		return Stats{}, fmt.Errorf("count records: %w", err)
	}
	stats.Records.Draft = stats.Records.Total - stats.Records.Final

	if err := s.countGrouped(ctx, `SELECT source, COUNT(*) FROM records GROUP BY source`, map[string]*int{
		"opencode": &stats.Records.BySource.OpenCode,
		"jarvis":   &stats.Records.BySource.Jarvis,
	}); err != nil {
		return Stats{}, fmt.Errorf("count records by source: %w", err)
	}
	if err := s.countGrouped(ctx, `SELECT card_status, COUNT(*) FROM records GROUP BY card_status`, map[string]*int{
		CardStatusHeuristic: &stats.Records.ByCardStatus.Heuristic,
		CardStatusQueued:    &stats.Records.ByCardStatus.Queued,
		CardStatusOK:        &stats.Records.ByCardStatus.OK,
		CardStatusFailed:    &stats.Records.ByCardStatus.Failed,
	}); err != nil {
		return Stats{}, fmt.Errorf("count records by card status: %w", err)
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&stats.Projects); err != nil {
		return Stats{}, fmt.Errorf("count projects: %w", err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM clients WHERE revoked_at IS NULL`,
	).Scan(&stats.Clients); err != nil {
		return Stats{}, fmt.Errorf("count clients: %w", err)
	}

	if err := s.countGrouped(ctx, `SELECT status, COUNT(*) FROM ingest_jobs GROUP BY status`, map[string]*int{
		string(JobQueued):  &stats.Jobs.Queued,
		string(JobRunning): &stats.Jobs.Running,
		string(JobDone):    &stats.Jobs.Done,
		string(JobFailed):  &stats.Jobs.Failed,
	}); err != nil {
		return Stats{}, fmt.Errorf("count jobs: %w", err)
	}

	var lastIngest sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM records`).Scan(&lastIngest); err != nil {
		return Stats{}, fmt.Errorf("read last ingest: %w", err)
	}
	lastIngestAt, err := parseNullableTime(lastIngest)
	if err != nil {
		return Stats{}, fmt.Errorf("read last ingest: %w", err)
	}
	stats.LastIngestAt = lastIngestAt

	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return Stats{}, err
	}
	size, err := s.databaseSize(ctx)
	if err != nil {
		return Stats{}, err
	}
	stats.Database = DatabaseStats{Status: "ok", SchemaVersion: version, SizeBytes: size}

	return stats, nil
}

// countGrouped applies COUNT(*) results to the destination for each known
// key. Unknown keys are ignored so a future status cannot break the endpoint.
func (s *Store) countGrouped(ctx context.Context, query string, dest map[string]*int) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key   string
			count int
		)
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		if target, ok := dest[key]; ok {
			*target = count
		}
	}
	return rows.Err()
}

func (s *Store) databaseSize(ctx context.Context) (int64, error) {
	var (
		pageCount int64
		pageSize  int64
	)
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("read page count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("read page size: %w", err)
	}
	return pageCount * pageSize, nil
}
