package finalizer_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/finalizer"
	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/SeRGei93/meme/server/internal/store"
)

func openFinalizerStore(t *testing.T) (*store.Store, int64) {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client, _, err := st.CreateClient(context.Background(), "finalizer", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	return st, client.ID
}

func ingestLiveSession(t *testing.T, st *store.Store, clientID int64, externalID string, updatedAt time.Time, final bool) int64 {
	t.Helper()

	bundle := ingest.Bundle{
		BundleID:   "bundle-" + externalID,
		ExternalID: externalID,
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session " + externalID,
		Final:      final,
		StartedAt:  updatedAt.Add(-time.Hour),
		UpdatedAt:  updatedAt,
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Question for " + externalID, At: updatedAt.Add(-time.Hour)},
		},
	}
	result, err := st.IngestSession(context.Background(), store.IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: bundle.BundleID,
		Bundle:         bundle,
		Now:            updatedAt,
	})
	if err != nil {
		t.Fatalf("IngestSession() error = %v", err)
	}
	return result.RecordID
}

func TestRunOnceFinalizesIdleSessionsAndEnqueuesCards(t *testing.T) {
	st, clientID := openFinalizerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	idle := ingestLiveSession(t, st, clientID, "ses_idle", now.Add(-31*time.Minute), false)
	recent := ingestLiveSession(t, st, clientID, "ses_recent", now.Add(-10*time.Minute), false)
	final := ingestLiveSession(t, st, clientID, "ses_final", now.Add(-5*time.Minute), true)

	before, err := st.ListJobs(ctx, store.ListJobsParams{Limit: 200})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}

	f := &finalizer.Finalizer{
		Store: st,
		Idle:  30 * time.Minute,
		Now:   func() time.Time { return now },
	}
	finalized, err := f.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if finalized != 1 {
		t.Errorf("RunOnce() = %d, want 1", finalized)
	}

	idleRecord, err := st.GetRecord(ctx, idle)
	if err != nil {
		t.Fatalf("GetRecord(idle) error = %v", err)
	}
	if !idleRecord.IsFinal || idleRecord.CardStatus != "queued" {
		t.Errorf("idle record = (is_final %v, card_status %q), want final and queued", idleRecord.IsFinal, idleRecord.CardStatus)
	}
	for name, id := range map[string]int64{"recent": recent, "final": final} {
		record, err := st.GetRecord(ctx, id)
		if err != nil {
			t.Fatalf("GetRecord(%s) error = %v", name, err)
		}
		if name == "recent" && record.IsFinal {
			t.Error("recent record was finalized before 30 minutes of silence")
		}
	}

	after, err := st.ListJobs(ctx, store.ListJobsParams{Limit: 200})
	if err != nil {
		t.Fatalf("ListJobs() after RunOnce error = %v", err)
	}
	if len(after.Items) != len(before.Items)+1 {
		t.Fatalf("jobs after RunOnce = %d, want %d", len(after.Items), len(before.Items)+1)
	}
	job := after.Items[0]
	if job.RecordID == nil || *job.RecordID != idle {
		t.Errorf("new job record_id = %v, want %d", job.RecordID, idle)
	}
	if job.Status != store.JobQueued {
		t.Errorf("new job status = %q, want queued", job.Status)
	}

	again, err := f.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if again != 0 {
		t.Errorf("second RunOnce() = %d, want 0", again)
	}
}
