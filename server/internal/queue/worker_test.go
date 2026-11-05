package queue_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/SeRGei93/meme/server/internal/queue"
	"github.com/SeRGei93/meme/server/internal/store"
)

type fakeProcessor struct {
	errs  []error
	calls []store.Job
}

func (f *fakeProcessor) Process(_ context.Context, job store.Job) error {
	f.calls = append(f.calls, job)
	if len(f.errs) == 0 {
		return nil
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return err
}

func openWorkerStore(t *testing.T) (*store.Store, int64) {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client, _, err := st.CreateClient(context.Background(), "queue-worker", auth.KindOpenCode, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	return st, client.ID
}

func enqueueJob(t *testing.T, st *store.Store, clientID int64, externalID string, at time.Time) int64 {
	t.Helper()

	bundle := ingest.Bundle{
		BundleID:   "bundle-" + externalID,
		ExternalID: externalID,
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session " + externalID,
		Final:      true,
		StartedAt:  at.Add(-time.Hour),
		UpdatedAt:  at,
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Question for " + externalID, At: at.Add(-time.Hour)},
		},
	}
	result, err := st.IngestSession(context.Background(), store.IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: bundle.BundleID,
		Bundle:         bundle,
		Now:            at,
	})
	if err != nil {
		t.Fatalf("IngestSession() error = %v", err)
	}
	if result.JobID == nil {
		t.Fatal("IngestSession() returned no job")
	}
	return *result.JobID
}

func TestWorkerProcessesJobAndMarksDone(t *testing.T) {
	st, clientID := openWorkerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_done", now)

	processor := &fakeProcessor{}
	worker := &queue.Worker{
		Store:        st,
		Processor:    processor,
		MaxAttempts:  5,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
	}

	processed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !processed {
		t.Fatal("RunOnce() = false, want the queued job")
	}
	if len(processor.calls) != 1 || processor.calls[0].ID != jobID {
		t.Fatalf("processor calls = %+v, want job %d", processor.calls, jobID)
	}

	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobDone {
		t.Errorf("job.Status = %q, want done", job.Status)
	}
	if job.Attempts != 1 {
		t.Errorf("job.Attempts = %d, want 1", job.Attempts)
	}
	if job.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil", job.NextRetryAt)
	}

	if processed, err := worker.RunOnce(ctx); err != nil || processed {
		t.Errorf("second RunOnce() = (%v, %v), want (false, nil)", processed, err)
	}
}

func TestWorkerRetriesTemporaryFailureWithBackoff(t *testing.T) {
	st, clientID := openWorkerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_retry", now)

	secret := "ghp_" + strings.Repeat("A", 36)
	processor := &fakeProcessor{errs: []error{
		errors.New("provider unavailable: " + secret),
		errors.New("provider timeout"),
		nil,
	}}
	var logs bytes.Buffer
	worker := &queue.Worker{
		Store:        st,
		Processor:    processor,
		MaxAttempts:  5,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
		Logger:       slog.New(slog.NewJSONHandler(&logs, nil)),
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobQueued || job.Attempts != 1 {
		t.Fatalf("job after first failure = (%q, attempts %d), want queued after 1 attempt", job.Status, job.Attempts)
	}
	if job.NextRetryAt == nil || !job.NextRetryAt.Equal(now.Add(time.Second)) {
		t.Errorf("job.NextRetryAt = %v, want %s", job.NextRetryAt, now.Add(time.Second))
	}
	if !strings.Contains(job.Error, "[REDACTED]") || strings.Contains(job.Error, secret) {
		t.Errorf("job.Error = %q, want a masked error", job.Error)
	}
	if strings.Contains(logs.String(), secret) {
		t.Errorf("worker log contains the secret: %s", logs.String())
	}

	if processed, err := worker.RunOnce(ctx); err != nil || processed {
		t.Errorf("early retry = (%v, %v), want (false, nil) before next_retry_at", processed, err)
	}

	now = now.Add(time.Second)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	job, err = st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobQueued || job.Attempts != 2 {
		t.Fatalf("job after second failure = (%q, attempts %d), want queued after 2 attempts", job.Status, job.Attempts)
	}
	if job.NextRetryAt == nil || !job.NextRetryAt.Equal(now.Add(2*time.Second)) {
		t.Errorf("job.NextRetryAt = %v, want %s", job.NextRetryAt, now.Add(2*time.Second))
	}

	now = now.Add(2 * time.Second)
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("third RunOnce() error = %v", err)
	}
	job, err = st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobDone || job.Attempts != 3 {
		t.Errorf("job after success = (%q, attempts %d), want done after 3 attempts", job.Status, job.Attempts)
	}
	if job.Error != "" {
		t.Errorf("job.Error = %q, want empty after success", job.Error)
	}
}

func TestWorkerFailsAfterMaxAttempts(t *testing.T) {
	st, clientID := openWorkerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_fail", now)

	processor := &fakeProcessor{errs: []error{
		errors.New("first"), errors.New("second"), errors.New("third"),
	}}
	worker := &queue.Worker{
		Store:        st,
		Processor:    processor,
		MaxAttempts:  3,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce(attempt %d) error = %v", attempt, err)
		}
		job, err := st.GetJob(ctx, jobID)
		if err != nil {
			t.Fatalf("GetJob() error = %v", err)
		}
		if attempt < 3 {
			if job.Status != store.JobQueued {
				t.Fatalf("job status after attempt %d = %q, want queued", attempt, job.Status)
			}
			if job.NextRetryAt == nil {
				t.Fatalf("job.NextRetryAt after attempt %d = nil, want a retry instant", attempt)
			}
			now = *job.NextRetryAt
		}
	}

	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobFailed {
		t.Errorf("job.Status = %q, want failed after %d attempts", job.Status, worker.MaxAttempts)
	}
	if job.Attempts != 3 {
		t.Errorf("job.Attempts = %d, want 3", job.Attempts)
	}
	if job.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil after terminal failure", job.NextRetryAt)
	}
	if job.Error != "third" {
		t.Errorf("job.Error = %q, want the final error", job.Error)
	}
	if job.RecordID == nil {
		t.Fatal("failed job has no record_id")
	}
	record, err := st.GetRecord(ctx, *job.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusFailed {
		t.Errorf("record.card_status = %q, want failed after terminal job failure", record.CardStatus)
	}

	if processed, err := worker.RunOnce(ctx); err != nil || processed {
		t.Errorf("RunOnce() after failure = (%v, %v), want (false, nil)", processed, err)
	}
}

func TestWorkerDoesNotRetryPermanentFailure(t *testing.T) {
	st, clientID := openWorkerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_permanent", now)

	processor := &fakeProcessor{errs: []error{
		queue.Permanent(errors.New("invalid card payload")),
	}}
	worker := &queue.Worker{
		Store:        st,
		Processor:    processor,
		MaxAttempts:  5,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
	}

	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobFailed {
		t.Errorf("job.Status = %q, want failed on a permanent error", job.Status)
	}
	if job.Attempts != 1 {
		t.Errorf("job.Attempts = %d, want 1", job.Attempts)
	}
	if job.NextRetryAt != nil {
		t.Errorf("job.NextRetryAt = %v, want nil", job.NextRetryAt)
	}
	if job.Error != "invalid card payload" {
		t.Errorf("job.Error = %q, want the wrapped error", job.Error)
	}
	if job.RecordID == nil {
		t.Fatal("failed job has no record_id")
	}
	record, err := st.GetRecord(ctx, *job.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusFailed {
		t.Errorf("record.card_status = %q, want failed on a permanent error", record.CardStatus)
	}
}

func TestWorkerRecoversRunningJobOnRestart(t *testing.T) {
	st, clientID := openWorkerStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_restart", now)

	claimed, ok, err := st.ClaimNextJob(ctx, now)
	if err != nil || !ok {
		t.Fatalf("ClaimNextJob() = (_, %v, %v), want the queued job", ok, err)
	}
	if claimed.ID != jobID {
		t.Fatalf("claimed job = %d, want %d", claimed.ID, jobID)
	}

	worker := &queue.Worker{
		Store:        st,
		Processor:    &fakeProcessor{},
		MaxAttempts:  5,
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
	}
	recovered, err := worker.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if recovered != 1 {
		t.Errorf("Recover() = %d, want 1", recovered)
	}

	job, err := st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobQueued || job.Attempts != 1 {
		t.Fatalf("job after recovery = (%q, attempts %d), want queued with the interrupted attempt preserved", job.Status, job.Attempts)
	}

	if processed, err := worker.RunOnce(ctx); err != nil || !processed {
		t.Fatalf("RunOnce() after recovery = (%v, %v), want the recovered job", processed, err)
	}
	job, err = st.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != store.JobDone || job.Attempts != 2 {
		t.Errorf("job after restart = (%q, attempts %d), want done after 2 attempts", job.Status, job.Attempts)
	}
}

func TestWorkerRunRecoversAndProcessesUntilCancelled(t *testing.T) {
	st, clientID := openWorkerStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	jobID := enqueueJob(t, st, clientID, "ses_run", now)

	// Simulate a process that died after claiming the job.
	if claimed, ok, err := st.ClaimNextJob(context.Background(), now); err != nil || !ok {
		t.Fatalf("ClaimNextJob() = (_, %v, %v), want the queued job", ok, err)
	} else if claimed.ID != jobID {
		t.Fatalf("claimed job = %d, want %d", claimed.ID, jobID)
	}

	processed := make(chan struct{}, 1)
	worker := &queue.Worker{
		Store: st,
		Processor: queue.ProcessorFunc(func(context.Context, store.Job) error {
			processed <- struct{}{}
			return nil
		}),
		PollInterval: 10 * time.Millisecond,
		MaxAttempts:  5,
		Now:          func() time.Time { return now },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()

	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not process the recovered job")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := st.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetJob() error = %v", err)
		}
		if job.Status == store.JobDone {
			if job.Attempts != 2 {
				t.Errorf("job.Attempts = %d, want 2", job.Attempts)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job after Run = (%q, attempts %d), want done after 2 attempts", job.Status, job.Attempts)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v, want nil after cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestBackoffGrowsExponentiallyAndCaps(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 32 * time.Second},
		{7, time.Minute},
		{8, time.Minute},
	}
	for _, tt := range tests {
		if got := queue.Backoff(tt.attempt); got != tt.want {
			t.Errorf("Backoff(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}
