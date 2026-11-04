package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/ingest"
)

func openJobStore(t *testing.T) (*Store, int64) {
	t.Helper()

	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client, _, err := st.CreateClient(context.Background(), "job-fixture", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	return st, client.ID
}

func insertJob(t *testing.T, st *Store, clientID int64, status JobStatus, attempts int, nextRetryAt *time.Time, jobError string) int64 {
	t.Helper()

	var next any
	if nextRetryAt != nil {
		next = formatTime(*nextRetryAt)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO ingest_jobs
			(client_id, idempotency_key, source, external_id, record_id, kind, status, attempts, next_retry_at, error, created_at, updated_at)
		VALUES (?, '', 'opencode', ?, NULL, 'ingest', ?, ?, ?, ?, ?, ?)`,
		clientID, "ses_"+time.Duration(attempts).String(), string(status), attempts, next, jobError,
		formatTime(now), formatTime(now),
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("insert job id: %v", err)
	}
	return id
}

func TestClaimNextJobMarksRunningAndIncrementsAttempts(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := insertJob(t, st, clientID, JobQueued, 0, nil, "")

	job, ok, err := st.ClaimNextJob(ctx, now)
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextJob() ok = false, want a queued job")
	}
	if job.ID != jobID {
		t.Errorf("job.ID = %d, want %d", job.ID, jobID)
	}
	if job.Status != JobRunning {
		t.Errorf("job.Status = %q, want running", job.Status)
	}
	if job.Attempts != 1 {
		t.Errorf("job.Attempts = %d, want 1 after the claim", job.Attempts)
	}
	if job.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil while running", job.NextRetryAt)
	}
	if !job.UpdatedAt.Equal(now) {
		t.Errorf("job.UpdatedAt = %s, want %s", job.UpdatedAt, now)
	}

	if _, ok, err := st.ClaimNextJob(ctx, now); err != nil || ok {
		t.Errorf("second ClaimNextJob() = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	if err := st.CompleteJob(ctx, jobID, now.Add(time.Second)); err != nil {
		t.Fatalf("CompleteJob() error = %v", err)
	}
	stored, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if stored.Status != JobDone {
		t.Errorf("job.Status = %q, want done", stored.Status)
	}
	if stored.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil after completion", stored.NextRetryAt)
	}
	if stored.Error != "" {
		t.Errorf("job.Error = %q, want empty after completion", stored.Error)
	}
}

func TestClaimNextJobWaitsForRetryTime(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	jobID := insertJob(t, st, clientID, JobQueued, 1, &retryAt, "temporary")

	if _, ok, err := st.ClaimNextJob(ctx, now); err != nil || ok {
		t.Fatalf("early ClaimNextJob() = (_, %v, %v), want no job before next_retry_at", ok, err)
	}
	job, ok, err := st.ClaimNextJob(ctx, retryAt.Add(time.Second))
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if !ok || job.ID != jobID {
		t.Fatalf("ClaimNextJob() = (%+v, %v), want job %d", job, ok, jobID)
	}
	if job.Attempts != 2 {
		t.Errorf("job.Attempts = %d, want 2", job.Attempts)
	}
}

func TestRetryAndFailJobPersistMaskedError(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := insertJob(t, st, clientID, JobQueued, 0, nil, "")
	if _, ok, err := st.ClaimNextJob(ctx, now); err != nil || !ok {
		t.Fatalf("ClaimNextJob() = (_, %v, %v), want the queued job", ok, err)
	}

	secret := "ghp_" + strings.Repeat("A", 36)
	retryAt := now.Add(2 * time.Second)
	if err := st.RetryJob(ctx, jobID, now, retryAt, "provider failed with "+secret); err != nil {
		t.Fatalf("RetryJob() error = %v", err)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != JobQueued {
		t.Errorf("job.Status = %q, want queued after retry", job.Status)
	}
	if job.NextRetryAt == nil || !job.NextRetryAt.Equal(retryAt) {
		t.Errorf("job.NextRetryAt = %v, want %s", job.NextRetryAt, retryAt)
	}
	if !strings.Contains(job.Error, "[REDACTED]") || strings.Contains(job.Error, secret) {
		t.Errorf("job.Error = %q, want a masked error", job.Error)
	}

	if _, ok, err := st.ClaimNextJob(ctx, retryAt); err != nil || !ok {
		t.Fatalf("ClaimNextJob() after retry = (_, %v, %v), want the job", ok, err)
	}
	if err := st.FailJob(ctx, jobID, now.Add(time.Second), "fatal "+secret); err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}
	job, err = st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != JobFailed {
		t.Errorf("job.Status = %q, want failed", job.Status)
	}
	if job.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil after failure", job.NextRetryAt)
	}
	if !strings.Contains(job.Error, "[REDACTED]") || strings.Contains(job.Error, secret) {
		t.Errorf("job.Error = %q, want a masked error", job.Error)
	}
}

func TestRecoverRunningJobsReturnsThemToQueue(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := insertJob(t, st, clientID, JobRunning, 2, nil, "")

	recovered, err := st.RecoverRunningJobs(ctx, now)
	if err != nil {
		t.Fatalf("RecoverRunningJobs() error = %v", err)
	}
	if recovered != 1 {
		t.Errorf("RecoverRunningJobs() = %d, want 1", recovered)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != JobQueued {
		t.Errorf("job.Status = %q, want queued after recovery", job.Status)
	}
	if job.Attempts != 2 {
		t.Errorf("job.Attempts = %d, want the interrupted attempt preserved", job.Attempts)
	}

	claimed, ok, err := st.ClaimNextJob(ctx, now)
	if err != nil || !ok {
		t.Fatalf("ClaimNextJob() = (_, %v, %v), want the recovered job", ok, err)
	}
	if claimed.Attempts != 3 {
		t.Errorf("claimed.Attempts = %d, want 3", claimed.Attempts)
	}
}

func TestListJobsNewestFirstFiltersAndPaginates(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	statuses := []JobStatus{JobQueued, JobDone, JobFailed, JobQueued, JobDone}
	ids := make([]int64, len(statuses))
	for i, status := range statuses {
		ids[i] = insertJob(t, st, clientID, status, 0, nil, "")
	}

	page, err := st.ListJobs(ctx, ListJobsParams{Limit: 2})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != ids[4] || page.Items[1].ID != ids[3] {
		t.Fatalf("first page ids = %v, want [%d %d]", jobIDs(page.Items), ids[4], ids[3])
	}
	if !page.HasMore {
		t.Error("first page HasMore = false, want true")
	}

	page, err = st.ListJobs(ctx, ListJobsParams{Limit: 2, BeforeID: page.Items[len(page.Items)-1].ID})
	if err != nil {
		t.Fatalf("second ListJobs() error = %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != ids[2] || page.Items[1].ID != ids[1] {
		t.Fatalf("second page ids = %v, want [%d %d]", jobIDs(page.Items), ids[2], ids[1])
	}
	if !page.HasMore {
		t.Error("second page HasMore = false, want true")
	}

	page, err = st.ListJobs(ctx, ListJobsParams{Limit: 2, BeforeID: page.Items[len(page.Items)-1].ID})
	if err != nil {
		t.Fatalf("third ListJobs() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != ids[0] {
		t.Fatalf("third page ids = %v, want [%d]", jobIDs(page.Items), ids[0])
	}
	if page.HasMore {
		t.Error("third page HasMore = true, want false")
	}

	failed, err := st.ListJobs(ctx, ListJobsParams{Status: JobFailed})
	if err != nil {
		t.Fatalf("ListJobs(failed) error = %v", err)
	}
	if len(failed.Items) != 1 || failed.Items[0].ID != ids[2] {
		t.Errorf("failed ids = %v, want [%d]", jobIDs(failed.Items), ids[2])
	}

	if _, err := st.ListJobs(ctx, ListJobsParams{Status: "unknown"}); !errors.Is(err, ErrInvalidJobQuery) {
		t.Errorf("ListJobs(unknown status) error = %v, want ErrInvalidJobQuery", err)
	}
	if _, err := st.ListJobs(ctx, ListJobsParams{Limit: 201}); !errors.Is(err, ErrInvalidJobQuery) {
		t.Errorf("ListJobs(limit 201) error = %v, want ErrInvalidJobQuery", err)
	}
	if _, err := st.ListJobs(ctx, ListJobsParams{BeforeID: -1}); !errors.Is(err, ErrInvalidJobQuery) {
		t.Errorf("ListJobs(before -1) error = %v, want ErrInvalidJobQuery", err)
	}
}

func TestGetJobReturnsNotFound(t *testing.T) {
	st, _ := openJobStore(t)

	if _, err := st.GetJob(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob(999) error = %v, want ErrNotFound", err)
	}
}

func TestFinalizeIdleSessionsMarksFinalAndEnqueuesJob(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	old := ingestSession(t, st, clientID, "old-live", "ses_old", now.Add(-31*time.Minute), false)
	recent := ingestSession(t, st, clientID, "recent-live", "ses_recent", now.Add(-10*time.Minute), false)
	final := ingestSession(t, st, clientID, "already-final", "ses_final", now.Add(-5*time.Minute), true)
	memory := insertMemoryRecord(t, st, clientID, now.Add(-2*time.Hour))

	before, err := st.ListJobs(ctx, ListJobsParams{Limit: 200})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}

	finalized, err := st.FinalizeIdleSessions(ctx, now, 30*time.Minute)
	if err != nil {
		t.Fatalf("FinalizeIdleSessions() error = %v", err)
	}
	if finalized != 1 {
		t.Errorf("FinalizeIdleSessions() = %d, want only the 31-minute-old session", finalized)
	}

	oldRecord, err := st.GetRecord(ctx, old)
	if err != nil {
		t.Fatalf("GetRecord(old) error = %v", err)
	}
	if !oldRecord.IsFinal {
		t.Error("old record is_final = false, want true")
	}
	if oldRecord.FinalizedAt == nil || !oldRecord.FinalizedAt.Equal(now) {
		t.Errorf("old record finalized_at = %v, want %s", oldRecord.FinalizedAt, now)
	}
	if oldRecord.CardStatus != CardStatusQueued {
		t.Errorf("old record card_status = %q, want queued", oldRecord.CardStatus)
	}
	if oldRecord.Revision != 2 {
		t.Errorf("old record revision = %d, want 2 after finalization", oldRecord.Revision)
	}

	memoryRecord, err := st.GetRecord(ctx, memory)
	if err != nil {
		t.Fatalf("GetRecord(memory) error = %v", err)
	}
	if memoryRecord.IsFinal {
		t.Error("memory record is_final = true, want finalizer to ignore non-session records")
	}

	for name, id := range map[string]int64{"recent": recent, "final": final} {
		record, err := st.GetRecord(ctx, id)
		if err != nil {
			t.Fatalf("GetRecord(%s) error = %v", name, err)
		}
		if name == "recent" && record.IsFinal {
			t.Error("recent record is_final = true, want false before the idle timeout")
		}
		if name == "final" && !record.IsFinal {
			t.Error("already final record is_final = false, want true")
		}
	}

	after, err := st.ListJobs(ctx, ListJobsParams{Limit: 200})
	if err != nil {
		t.Fatalf("ListJobs() after finalize error = %v", err)
	}
	if len(after.Items) != len(before.Items)+1 {
		t.Fatalf("job count after finalize = %d, want %d", len(after.Items), len(before.Items)+1)
	}
	job := after.Items[0]
	if job.RecordID == nil || *job.RecordID != old {
		t.Errorf("finalizer job record_id = %v, want %d", job.RecordID, old)
	}
	if job.Kind != "ingest" || job.Status != JobQueued {
		t.Errorf("finalizer job = %+v, want kind ingest and status queued", job)
	}
	if !job.CreatedAt.Equal(now) {
		t.Errorf("finalizer job created_at = %s, want %s", job.CreatedAt, now)
	}

	again, err := st.FinalizeIdleSessions(ctx, now.Add(time.Minute), 30*time.Minute)
	if err != nil {
		t.Fatalf("second FinalizeIdleSessions() error = %v", err)
	}
	if again != 0 {
		t.Errorf("second FinalizeIdleSessions() = %d, want 0", again)
	}
	afterSecond, err := st.ListJobs(ctx, ListJobsParams{Limit: 200})
	if err != nil {
		t.Fatalf("ListJobs() after second finalize error = %v", err)
	}
	if len(afterSecond.Items) != len(after.Items) {
		t.Errorf("second finalize created jobs: %d -> %d", len(after.Items), len(afterSecond.Items))
	}
}

func ingestSession(t *testing.T, st *Store, clientID int64, bundleID, externalID string, updatedAt time.Time, final bool) int64 {
	t.Helper()

	bundle := ingest.Bundle{
		BundleID:   bundleID,
		ExternalID: externalID,
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session " + externalID,
		Final:      final,
		StartedAt:  updatedAt.Add(-time.Hour),
		UpdatedAt:  updatedAt,
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Question " + externalID, At: updatedAt.Add(-time.Hour)},
		},
	}
	result, err := st.IngestSession(context.Background(), IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: bundleID,
		Bundle:         bundle,
		Now:            updatedAt,
	})
	if err != nil {
		t.Fatalf("IngestSession(%s) error = %v", externalID, err)
	}
	return result.RecordID
}

func insertMemoryRecord(t *testing.T, st *Store, clientID int64, updatedAt time.Time) int64 {
	t.Helper()

	result, err := st.db.ExecContext(context.Background(), `
		INSERT INTO records (
			client_id, source, external_id, project_id, kind, title, summary,
			questions, keywords, atoms, raw_text, content_hash, revision,
			is_final, finalized_at, started_at, updated_at, indexed_at,
			card_model, card_status, metadata_json
		) VALUES (?, 'jarvis', 'mem_idle', NULL, 'memory', 'Memory', '', '', '', '',
			'raw memory text', 'memory-hash', 1, 0, NULL, NULL, ?, ?, NULL, 'heuristic', '{}')`,
		clientID, formatTime(updatedAt), formatTime(updatedAt),
	)
	if err != nil {
		t.Fatalf("insert memory record: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("insert memory record id: %v", err)
	}
	return id
}

func jobIDs(jobs []Job) []int64 {
	ids := make([]int64, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
	}
	return ids
}
