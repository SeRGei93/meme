package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/mcp"
	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/store"
)

// mcpBackend adapts the existing store services to the five MCP tools. Every
// response shape is the same Go type the REST handlers serialize, so the two
// surfaces cannot drift.
type mcpBackend struct {
	server *server
}

type searchArguments struct {
	Query   string  `json:"query"`
	Limit   *int    `json:"limit"`
	Project *string `json:"project"`
	Source  *string `json:"source"`
}

type getRecordArguments struct {
	ID int64 `json:"id"`
}

type rememberArguments struct {
	Text       string     `json:"text"`
	Title      *string    `json:"title"`
	Summary    *string    `json:"summary"`
	Project    *string    `json:"project"`
	Tags       []string   `json:"tags"`
	ExternalID *string    `json:"external_id"`
	OccurredAt *time.Time `json:"occurred_at"`
}

type listProjectsArguments struct {
	Limit *int `json:"limit"`
}

func (b mcpBackend) Search(ctx context.Context, _ mcp.CallContext, raw json.RawMessage) (any, *mcp.ToolError) {
	var args searchArguments
	if toolErr := decodeToolArguments(raw, &args); toolErr != nil {
		return nil, toolErr
	}
	if toolErr := rejectNullArguments(raw, "limit", "project", "source"); toolErr != nil {
		return nil, toolErr
	}

	query := strings.TrimSpace(args.Query)
	if runeLen(query) < 1 || runeLen(query) > 500 {
		return nil, validationError("query", "must be 1-500 characters")
	}
	limit := 8
	if args.Limit != nil {
		limit = *args.Limit
		if limit < 1 || limit > 50 {
			return nil, validationError("limit", "must be between 1 and 50")
		}
	}
	project := ""
	if args.Project != nil {
		project = strings.TrimSpace(*args.Project)
		if runeLen(project) < 1 || runeLen(project) > 500 {
			return nil, validationError("project", "must be 1-500 characters")
		}
	}
	source := ""
	if args.Source != nil {
		source = strings.TrimSpace(*args.Source)
		if runeLen(source) < 1 || runeLen(source) > auth.MaxSourceQueryLen {
			return nil, validationError("source", "must be 1-"+strconv.Itoa(auth.MaxSourceQueryLen)+" characters")
		}
	}

	result, err := b.server.store.Search(ctx, store.SearchParams{
		Query:      query,
		Limit:      limit,
		ProjectKey: project,
		Source:     source,
		Now:        time.Now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrInvalidSearch):
		return nil, &mcp.ToolError{Code: "invalid_request", Message: err.Error()}
	case err != nil:
		b.server.logger.Error("mcp search", "error", err, "request_id", requestIDFrom(ctx))
		return nil, internalError()
	}

	return toSearchResponse(result, query, limit), nil
}

func (b mcpBackend) GetRecord(ctx context.Context, _ mcp.CallContext, raw json.RawMessage) (any, *mcp.ToolError) {
	var args getRecordArguments
	if toolErr := decodeToolArguments(raw, &args); toolErr != nil {
		return nil, toolErr
	}
	if args.ID < 1 {
		return nil, validationError("id", "must be a positive integer")
	}

	record, err := b.server.store.GetRecord(ctx, args.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, &mcp.ToolError{Code: "not_found", Message: fmt.Sprintf("Record %d not found.", args.ID)}
	case err != nil:
		b.server.logger.Error("mcp get record", "error", err, "record_id", args.ID, "request_id", requestIDFrom(ctx))
		return nil, internalError()
	}
	return toRecordResponse(record), nil
}

func (b mcpBackend) Remember(ctx context.Context, call mcp.CallContext, raw json.RawMessage) (any, *mcp.ToolError) {
	var args rememberArguments
	if toolErr := decodeToolArguments(raw, &args); toolErr != nil {
		return nil, toolErr
	}
	if toolErr := rejectNullArguments(raw, "text", "title", "summary", "project", "tags", "external_id", "occurred_at"); toolErr != nil {
		return nil, toolErr
	}

	request, toolErr := rememberRequest(args)
	if toolErr != nil {
		return nil, toolErr
	}

	result, err := b.server.store.SaveMemory(ctx, store.MemoryParams{
		ClientID:       call.ClientID,
		Source:         call.Source,
		IdempotencyKey: request.ExternalIDOrAuto(),
		Request:        request,
		Now:            time.Now().UTC(),
	})
	switch {
	case errors.Is(err, store.ErrMemoryConflict):
		return nil, &mcp.ToolError{Code: "conflict", Message: "external_id already exists with different content"}
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		return nil, &mcp.ToolError{Code: "conflict", Message: "external_id was already used with a different request"}
	case err != nil:
		b.server.logger.Error("mcp remember", "error", err, "client_id", call.ClientID, "request_id", requestIDFrom(ctx))
		return nil, internalError()
	}

	return memoryResponse{
		RecordID: result.RecordID,
		JobID:    result.JobID,
		Status:   result.Status,
		Replayed: result.Replayed,
	}, nil
}

func rememberRequest(args rememberArguments) (memory.Request, *mcp.ToolError) {
	if runeLen(args.Text) < 1 || runeLen(args.Text) > memory.MaxRawTextRunes {
		return memory.Request{}, validationError("text", "must be 1-2000000 characters")
	}
	if args.Title != nil && (runeLen(*args.Title) < 1 || runeLen(*args.Title) > 500) {
		return memory.Request{}, validationError("title", "must be 1-500 characters")
	}
	if args.Summary != nil && runeLen(*args.Summary) > 5_000 {
		return memory.Request{}, validationError("summary", "must be at most 5000 characters")
	}
	project := memory.DefaultProjectKey
	if args.Project != nil {
		if runeLen(*args.Project) < 1 || runeLen(*args.Project) > 500 {
			return memory.Request{}, validationError("project", "must be 1-500 characters")
		}
		project = *args.Project
	}
	if len(args.Tags) > 100 {
		return memory.Request{}, validationError("tags", "must contain at most 100 items")
	}
	for _, tag := range args.Tags {
		if runeLen(tag) < 1 || runeLen(tag) > 200 {
			return memory.Request{}, validationError("tags", "each item must be 1-200 characters")
		}
	}
	if args.ExternalID != nil && (runeLen(*args.ExternalID) < 1 || runeLen(*args.ExternalID) > 200) {
		return memory.Request{}, validationError("external_id", "must be 1-200 characters")
	}

	request := memory.Request{
		RawText:    args.Text,
		ProjectKey: project,
		Keywords:   args.Tags,
	}
	if args.Title != nil {
		request.Title = *args.Title
	}
	if args.Summary != nil {
		request.Summary = *args.Summary
	}
	if args.ExternalID != nil {
		request.ExternalID = *args.ExternalID
	}
	if args.OccurredAt != nil {
		request.OccurredAt = args.OccurredAt
	}
	if err := request.Validate(); err != nil {
		return memory.Request{}, &mcp.ToolError{Code: "validation_failed", Message: err.Error()}
	}
	return request, nil
}

func (b mcpBackend) ListProjects(ctx context.Context, _ mcp.CallContext, raw json.RawMessage) (any, *mcp.ToolError) {
	var args listProjectsArguments
	if toolErr := decodeToolArguments(raw, &args); toolErr != nil {
		return nil, toolErr
	}
	if toolErr := rejectNullArguments(raw, "limit"); toolErr != nil {
		return nil, toolErr
	}
	limit := store.DefaultProjectLimit
	if args.Limit != nil {
		limit = *args.Limit
		if limit < 1 || limit > store.MaxProjectLimit {
			return nil, validationError("limit", fmt.Sprintf("must be between 1 and %d", store.MaxProjectLimit))
		}
	}

	projects, err := b.server.store.ListProjects(ctx, store.ListProjectsParams{Limit: limit})
	if errors.Is(err, store.ErrInvalidProjectQuery) {
		return nil, &mcp.ToolError{Code: "invalid_request", Message: err.Error()}
	}
	if err != nil {
		b.server.logger.Error("mcp list projects", "error", err, "request_id", requestIDFrom(ctx))
		return nil, internalError()
	}
	return toProjectListResponse(projects), nil
}

func (b mcpBackend) Stats(ctx context.Context, _ mcp.CallContext, raw json.RawMessage) (any, *mcp.ToolError) {
	var args struct{}
	if toolErr := decodeToolArguments(raw, &args); toolErr != nil {
		return nil, toolErr
	}

	stats, err := b.server.store.Stats(ctx)
	if err != nil {
		b.server.logger.Error("mcp stats", "error", err, "request_id", requestIDFrom(ctx))
		return nil, internalError()
	}
	return toStatsResponse(stats), nil
}

func validationError(field, message string) *mcp.ToolError {
	return &mcp.ToolError{Code: "validation_failed", Message: field + " " + message}
}

func internalError() *mcp.ToolError {
	return &mcp.ToolError{Code: "internal_error", Message: "internal error"}
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}
