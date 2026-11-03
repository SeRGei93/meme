package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/ingest"
)

func TestIngestReplayReturnsExistingJobAfterCompletion(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	client, _, err := st.CreateClient(ctx, "opencode-capture", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	started := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	bundle := ingest.Bundle{
		BundleID:    "6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID:  "ses_01JABCDEF",
		ProjectKey:  "github.com/SeRGei93/meme",
		ProjectPath: "/Users/serg/PhpstormProjects/meme",
		Title:       "Fix SQLite migration ordering",
		Final:       true,
		StartedAt:   started,
		UpdatedAt:   started.Add(time.Hour),
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Why?", At: started},
			{ID: "msg_2", Role: ingest.RoleAssistant, Text: "Because.", At: started.Add(time.Minute)},
		},
		Metadata: ingest.Metadata{"agent": "build"},
	}

	params := IngestParams{
		ClientID:       client.ID,
		Source:         "opencode",
		IdempotencyKey: "bundle-1",
		Bundle:         bundle,
		Now:            started.Add(2 * time.Hour),
	}
	first, err := st.IngestSession(ctx, params)
	if err != nil {
		t.Fatalf("first IngestSession() error = %v", err)
	}
	if first.JobID == nil {
		t.Fatal("first IngestSession() job_id = nil, want a queued job")
	}

	if _, err := st.db.ExecContext(ctx, "UPDATE ingest_jobs SET status = 'done' WHERE id = ?", *first.JobID); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	params.IdempotencyKey = "bundle-2"
	replay, err := st.IngestSession(ctx, params)
	if err != nil {
		t.Fatalf("replay IngestSession() error = %v", err)
	}
	if !replay.Replayed {
		t.Error("replay.Replayed = false, want true")
	}
	if replay.RecordID != first.RecordID {
		t.Errorf("replay.RecordID = %d, want %d", replay.RecordID, first.RecordID)
	}
	if replay.JobID == nil || *replay.JobID != *first.JobID {
		t.Errorf("replay.JobID = %v, want the existing %d", replay.JobID, *first.JobID)
	}
	if replay.Status != "queued" {
		t.Errorf("replay.Status = %q, want queued for an existing card job", replay.Status)
	}
}
