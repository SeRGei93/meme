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

type jobBody struct {
	ID          int64      `json:"id"`
	ClientID    int64      `json:"client_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	ExternalID  string     `json:"external_id"`
	RecordID    *int64     `json:"record_id"`
	NextRetryAt *time.Time `json:"next_retry_at"`
	Error       *string    `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type jobListBody struct {
	Items      []jobBody `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

func doListJobs(t *testing.T, env *testEnv, token, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/jobs"
	if query != "" {
		path += "?" + query
	}
	return env.do(t, http.MethodGet, path, token, nil)
}

func decodeJobs(t *testing.T, rec *httptest.ResponseRecorder) jobListBody {
	t.Helper()
	var body jobListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode jobs response: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func seedJobs(t *testing.T, env *testEnv, ingestToken string, count int) []int64 {
	t.Helper()

	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		bundle := validBundle()
		bundle["bundle_id"] = fmt.Sprintf("10000000-0000-4000-8000-%012d", i)
		bundle["external_id"] = fmt.Sprintf("ses_job_%d", i)
		bundle["title"] = fmt.Sprintf("job fixture %d", i)
		rec := doIngest(t, env, ingestToken, bundle, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("ingest %d: status = %d (body %s)", i, rec.Code, rec.Body.String())
		}
		accepted := decodeIngestResponse(t, rec)
		if accepted.JobID == nil {
			t.Fatalf("ingest %d: job_id = nil", i)
		}
		ids = append(ids, *accepted.JobID)
	}
	return ids
}

func TestListJobsRequiresAdminScope(t *testing.T) {
	env := newTestEnv(t)
	uiToken := createClientToken(t, env, "ui-jobs", "ui", []string{"ui"})
	adminToken := createClientToken(t, env, "admin-jobs", "admin", []string{"admin"})

	if rec := doListJobs(t, env, "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", rec.Code)
	}
	if rec := doListJobs(t, env, uiToken, ""); rec.Code != http.StatusForbidden {
		t.Errorf("ui token: status = %d, want 403", rec.Code)
	}
	if rec := doListJobs(t, env, adminToken, ""); rec.Code != http.StatusOK {
		t.Errorf("admin token: status = %d, want 200", rec.Code)
	}
}

func TestListJobsReturnsQueueStateNewestFirst(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)
	ids := seedJobs(t, env, ingestToken, 3)

	rec := doListJobs(t, env, testAdminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeJobs(t, rec)
	if len(body.Items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(body.Items))
	}
	for i, want := range []int64{ids[2], ids[1], ids[0]} {
		if body.Items[i].ID != want {
			t.Errorf("items[%d].id = %d, want %d (newest first)", i, body.Items[i].ID, want)
		}
	}
	if body.NextCursor != nil {
		t.Errorf("next_cursor = %v, want null on the last page", *body.NextCursor)
	}

	first := body.Items[0]
	if first.Kind != "ingest" || first.Status != "queued" || first.Attempts != 0 {
		t.Errorf("first job = (kind %q, status %q, attempts %d), want ingest/queued/0", first.Kind, first.Status, first.Attempts)
	}
	if first.RecordID == nil || *first.RecordID < 1 {
		t.Errorf("first.RecordID = %v, want a record id", first.RecordID)
	}
	if first.Error != nil {
		t.Errorf("first.Error = %v, want null", *first.Error)
	}
	if first.NextRetryAt != nil {
		t.Errorf("first.NextRetryAt = %v, want null", first.NextRetryAt)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() {
		t.Errorf("job timestamps are zero: %+v", first)
	}

	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw jobs: %v", err)
	}
	for _, field := range []string{"source", "idempotency_key"} {
		if _, ok := raw.Items[0][field]; ok {
			t.Errorf("job response exposes %q", field)
		}
	}
}

func TestListJobsPaginatesWithOpaqueCursor(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)
	ids := seedJobs(t, env, ingestToken, 5)

	first := decodeJobs(t, doListJobs(t, env, testAdminToken, "limit=2"))
	if len(first.Items) != 2 || first.Items[0].ID != ids[4] || first.Items[1].ID != ids[3] {
		t.Fatalf("first page = %+v, want ids [%d %d]", first.Items, ids[4], ids[3])
	}
	if first.NextCursor == nil {
		t.Fatal("first page next_cursor = null, want a cursor")
	}

	second := decodeJobs(t, doListJobs(t, env, testAdminToken, "limit=2&cursor="+url.QueryEscape(*first.NextCursor)))
	if len(second.Items) != 2 || second.Items[0].ID != ids[2] || second.Items[1].ID != ids[1] {
		t.Fatalf("second page = %+v, want ids [%d %d]", second.Items, ids[2], ids[1])
	}
	if second.NextCursor == nil {
		t.Fatal("second page next_cursor = null, want a cursor")
	}

	last := decodeJobs(t, doListJobs(t, env, testAdminToken, "limit=2&cursor="+url.QueryEscape(*second.NextCursor)))
	if len(last.Items) != 1 || last.Items[0].ID != ids[0] {
		t.Fatalf("last page = %+v, want id %d", last.Items, ids[0])
	}
	if last.NextCursor != nil {
		t.Errorf("last page next_cursor = %v, want null", *last.NextCursor)
	}
}

func TestListJobsFiltersByStatusAndShowsMaskedErrors(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)
	ids := seedJobs(t, env, ingestToken, 3)
	ctx := t.Context()
	now := time.Now().UTC()

	done, ok, err := env.store.ClaimNextJob(ctx, now)
	if err != nil || !ok {
		t.Fatalf("claim done job = (_, %v, %v)", ok, err)
	}
	if done.ID != ids[0] {
		t.Fatalf("claimed done job = %d, want %d", done.ID, ids[0])
	}
	if err := env.store.CompleteJob(ctx, done.ID, now); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}

	retried, ok, err := env.store.ClaimNextJob(ctx, now)
	if err != nil || !ok {
		t.Fatalf("claim retried job = (_, %v, %v)", ok, err)
	}
	secret := "sk-" + strings.Repeat("A", 24)
	if err := env.store.RetryJob(ctx, retried.ID, now, now.Add(time.Minute), "llm failed: "+secret); err != nil {
		t.Fatalf("RetryJob() error = %v", err)
	}

	failed, ok, err := env.store.ClaimNextJob(ctx, now)
	if err != nil || !ok {
		t.Fatalf("claim failed job = (_, %v, %v)", ok, err)
	}
	if err := env.store.FailJob(ctx, failed.ID, now, "permanent failure"); err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}

	tests := []struct {
		status string
		want   []int64
	}{
		{"queued", []int64{retried.ID}},
		{"done", []int64{done.ID}},
		{"failed", []int64{failed.ID}},
		{"running", nil},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			body := decodeJobs(t, doListJobs(t, env, testAdminToken, "status="+tt.status))
			if len(body.Items) != len(tt.want) {
				t.Fatalf("len(items) = %d, want %d (%+v)", len(body.Items), len(tt.want), body.Items)
			}
			for i, want := range tt.want {
				if body.Items[i].ID != want {
					t.Errorf("items[%d].id = %d, want %d", i, body.Items[i].ID, want)
				}
			}
		})
	}

	queued := decodeJobs(t, doListJobs(t, env, testAdminToken, "status=queued"))
	if queued.Items[0].Error == nil {
		t.Fatal("queued job error = null, want the retry error")
	}
	if !strings.Contains(*queued.Items[0].Error, "[REDACTED]") || strings.Contains(*queued.Items[0].Error, secret) {
		t.Errorf("queued job error = %q, want a masked error", *queued.Items[0].Error)
	}
	if queued.Items[0].NextRetryAt == nil {
		t.Error("queued job next_retry_at = null, want the scheduled retry")
	}
}

func TestListJobsValidatesParameters(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name  string
		query string
		field string
	}{
		{"unknown status", "status=pending", "status"},
		{"empty status", "status=", "status"},
		{"limit zero", "limit=0", "limit"},
		{"limit too large", "limit=201", "limit"},
		{"limit not a number", "limit=abc", "limit"},
		{"invalid cursor", "cursor=not-a-cursor", "cursor"},
		{"negative cursor", "cursor=" + url.QueryEscape("-1"), "cursor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doListJobs(t, env, testAdminToken, tt.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != "invalid_request" {
				t.Errorf("code = %q, want invalid_request", envelope.Error.Code)
			}
			if got := envelope.Error.Details["field"]; got != tt.field {
				t.Errorf("details.field = %v, want %q", got, tt.field)
			}
		})
	}
}

func TestListJobsReturnsEmptyArray(t *testing.T) {
	env := newTestEnv(t)

	rec := doListJobs(t, env, testAdminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decodeJobs(t, rec)
	if body.Items == nil {
		t.Error("items = null, want an empty array")
	}
	if len(body.Items) != 0 || body.NextCursor != nil {
		t.Errorf("body = %+v, want no items and no cursor", body)
	}
}
