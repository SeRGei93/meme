package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidProjectQuery is returned when a project listing violates the v1
// contract.
var ErrInvalidProjectQuery = errors.New("invalid project query")

const (
	// DefaultProjectLimit is the contract's page size when limit is omitted.
	DefaultProjectLimit = 100
	// MaxProjectLimit is the contract's upper bound for GET /api/v1/projects.
	MaxProjectLimit = 500
)

// Project is one row of GET /api/v1/projects with its activity summary.
type Project struct {
	Key            string
	Name           string
	Path           string
	RecordCount    int
	LastActivityAt *time.Time
	CreatedAt      time.Time
}

// ListProjectsParams controls the project listing. Limit defaults to 100.
type ListProjectsParams struct {
	Limit int
}

// ListProjects returns projects ordered by most recent activity first. A
// project without records falls back to its creation time for ordering and
// keeps last_activity_at empty.
func (s *Store) ListProjects(ctx context.Context, params ListProjectsParams) ([]Project, error) {
	limit := params.Limit
	if limit == 0 {
		limit = DefaultProjectLimit
	}
	if limit < 1 || limit > MaxProjectLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidProjectQuery, MaxProjectLimit)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.key, p.name, p.path, p.created_at,
			COUNT(r.id) AS record_count,
			MAX(r.updated_at) AS last_activity_at
		FROM projects p
		LEFT JOIN records r ON r.project_id = p.id
		GROUP BY p.id
		ORDER BY COALESCE(MAX(r.updated_at), p.created_at) DESC, p.key ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := []Project{}
	for rows.Next() {
		var (
			project      Project
			createdAt    string
			lastActivity sql.NullString
		)
		if err := rows.Scan(
			&project.Key, &project.Name, &project.Path, &createdAt,
			&project.RecordCount, &lastActivity,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		project.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan project %q: %w", project.Key, err)
		}
		project.LastActivityAt, err = parseNullableTime(lastActivity)
		if err != nil {
			return nil, fmt.Errorf("scan project %q: %w", project.Key, err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}
