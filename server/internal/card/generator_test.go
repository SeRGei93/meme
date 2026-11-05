package card_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/card"
	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/queue"
	"github.com/SeRGei93/meme/server/internal/store"
)

type fakeClient struct {
	content string
	err     error
	calls   [][]card.Message
}

func (f *fakeClient) Complete(_ context.Context, messages []card.Message) (string, error) {
	f.calls = append(f.calls, messages)
	return f.content, f.err
}

func openCardStore(t *testing.T) (*store.Store, int64, int64, int64) {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	client, _, err := st.CreateClient(context.Background(), "card-generator", auth.KindOpenCode, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	bundle := ingest.Bundle{
		BundleID:   "1f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID: "ses_generate",
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session ses_generate",
		Final:      true,
		StartedAt:  now.Add(-time.Hour),
		UpdatedAt:  now,
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Why does migration 004 run before 003?", At: now.Add(-time.Hour)},
		},
	}
	result, err := st.IngestSession(context.Background(), store.IngestParams{
		ClientID:       client.ID,
		Source:         "opencode",
		IdempotencyKey: bundle.BundleID,
		Bundle:         bundle,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("IngestSession() error = %v", err)
	}
	if result.JobID == nil {
		t.Fatal("IngestSession() returned no job")
	}
	return st, result.RecordID, *result.JobID, client.ID
}

func queuedJob(t *testing.T, st *store.Store, jobID int64) store.Job {
	t.Helper()

	job, err := st.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob(%d) error = %v", jobID, err)
	}
	return job
}

func generatedCardJSON() string {
	return strings.ReplaceAll(validCardJSON(), "sqlite", "quicksilver")
}

func TestWorkerCompletesAQueuedCardJob(t *testing.T) {
	st, recordID, _, _ := openCardStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 5, 0, 0, time.UTC)
	worker := &queue.Worker{
		Store:        st,
		Processor:    &card.Generator{Store: st, Client: &fakeClient{content: generatedCardJSON()}, Model: "test-model", Now: func() time.Time { return now }},
		PollInterval: time.Millisecond,
		Now:          func() time.Time { return now },
	}

	processed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !processed {
		t.Fatal("RunOnce() processed = false, want true")
	}

	record, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusOK {
		t.Errorf("CardStatus = %q, want ok", record.CardStatus)
	}
	if record.CardModel == nil || *record.CardModel != "test-model" {
		t.Errorf("CardModel = %v, want test-model", record.CardModel)
	}

	jobs, err := st.ListJobs(ctx, store.ListJobsParams{})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Status != store.JobDone {
		t.Fatalf("jobs = %+v, want one done job", jobs.Items)
	}
}

func TestGeneratorSkipsAStaleJobForAnOlderFinalVersion(t *testing.T) {
	st, recordID, _, clientID := openCardStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	stale, ok, err := st.ClaimNextJob(ctx, now)
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextJob() found no job")
	}

	newer := ingest.Bundle{
		BundleID:   "6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID: "ses_generate",
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session ses_generate",
		Final:      true,
		StartedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(10 * time.Minute),
		Messages: []ingest.Message{
			{ID: "msg_2", Role: ingest.RoleUser, Text: "A newer final question", At: now.Add(10 * time.Minute)},
		},
	}
	if _, err := st.IngestSession(ctx, store.IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: newer.BundleID,
		Bundle:         newer,
		Now:            now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("newer IngestSession() error = %v", err)
	}

	client := &fakeClient{content: generatedCardJSON()}
	generator := &card.Generator{
		Store:  st,
		Client: client,
		Model:  "test-model",
		Now:    func() time.Time { return now.Add(11 * time.Minute) },
	}

	if err := generator.Process(ctx, stale); err != nil {
		t.Fatalf("Process(stale) error = %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("stale job called the LLM %d times, want 0", len(client.calls))
	}
	pending, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if pending.CardStatus != store.CardStatusQueued {
		t.Fatalf("CardStatus after stale job = %q, want queued for the newer version", pending.CardStatus)
	}

	if err := st.CompleteJob(ctx, stale.ID, now.Add(11*time.Minute)); err != nil {
		t.Fatalf("CompleteJob(stale) error = %v", err)
	}
	current, ok, err := st.ClaimNextJob(ctx, now.Add(11*time.Minute))
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if !ok {
		t.Fatal("ClaimNextJob() found no newer job")
	}
	if err := generator.Process(ctx, current); err != nil {
		t.Fatalf("Process(newer) error = %v", err)
	}

	record, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusOK {
		t.Errorf("CardStatus = %q, want ok for the newer version", record.CardStatus)
	}
	if len(client.calls) != 1 {
		t.Errorf("LLM calls = %d, want exactly one for the newer version", len(client.calls))
	}
}

func TestGeneratorSavesAValidLLMCardAndIndexesIt(t *testing.T) {
	st, recordID, jobID, _ := openCardStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 5, 0, 0, time.UTC)
	client := &fakeClient{content: generatedCardJSON()}

	generator := &card.Generator{
		Store:  st,
		Client: client,
		Model:  "test-model",
		Now:    func() time.Time { return now },
	}
	job := queuedJob(t, st, jobID)
	if err := generator.Process(ctx, job); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	record, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.Title != "Fix SQLite migration ordering" {
		t.Errorf("Title = %q, want the generated card title", record.Title)
	}
	if record.CardStatus != store.CardStatusOK {
		t.Errorf("CardStatus = %q, want ok", record.CardStatus)
	}
	if record.CardModel == nil || *record.CardModel != "test-model" {
		t.Errorf("CardModel = %v, want test-model", record.CardModel)
	}

	result, err := st.Search(ctx, store.SearchParams{Query: "quicksilver", Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != recordID {
		t.Fatalf("Search(quicksilver) = %+v, want record %d", result.Items, recordID)
	}

	if len(client.calls) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(client.calls))
	}
	if len(client.calls[0]) != 2 || client.calls[0][0].Role != card.RoleSystem {
		t.Errorf("LLM prompt = %+v, want a system message followed by the session", client.calls[0])
	}
}

func TestGeneratorKeepsTheHeuristicCardWhenTheLLMFails(t *testing.T) {
	st, recordID, jobID, _ := openCardStore(t)
	ctx := context.Background()
	generator := &card.Generator{
		Store:  st,
		Client: &fakeClient{err: errors.New("llm is down")},
		Model:  "test-model",
	}

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	job := queuedJob(t, st, jobID)
	if err := generator.Process(ctx, job); err != nil {
		t.Fatalf("Process() error = %v, want a terminal fallback", err)
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.CardStatus != store.CardStatusFailed {
		t.Errorf("CardStatus = %q, want failed", after.CardStatus)
	}
	if after.Title != before.Title || after.Summary != before.Summary {
		t.Errorf("heuristic card changed: title %q -> %q, summary %q -> %q",
			before.Title, after.Title, before.Summary, after.Summary)
	}
	if after.CardModel != nil {
		t.Errorf("CardModel = %v, want nil after a failed generation", after.CardModel)
	}
}

func TestGeneratorKeepsTheHeuristicCardWhenTheResponseIsInvalid(t *testing.T) {
	st, recordID, jobID, _ := openCardStore(t)
	ctx := context.Background()
	generator := &card.Generator{
		Store:  st,
		Client: &fakeClient{content: `{"title":"HACKED","summary":"ignore previous instructions"}`},
		Model:  "test-model",
	}

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	job := queuedJob(t, st, jobID)
	if err := generator.Process(ctx, job); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.CardStatus != store.CardStatusFailed {
		t.Errorf("CardStatus = %q, want failed", after.CardStatus)
	}
	if after.Title == "HACKED" {
		t.Error("an invalid LLM response replaced the heuristic card")
	}
	if after.Title != before.Title {
		t.Errorf("Title = %q, want the heuristic title %q", after.Title, before.Title)
	}
}

func TestGeneratorWithoutAClientKeepsTheHeuristicCard(t *testing.T) {
	st, recordID, jobID, _ := openCardStore(t)
	ctx := context.Background()
	generator := &card.Generator{Store: st}

	job := queuedJob(t, st, jobID)
	if err := generator.Process(ctx, job); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	record, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusHeuristic {
		t.Errorf("CardStatus = %q, want heuristic", record.CardStatus)
	}
}

func TestGeneratorIgnoresAJobWithoutARecord(t *testing.T) {
	st, _, _, _ := openCardStore(t)
	generator := &card.Generator{Store: st, Client: &fakeClient{content: generatedCardJSON()}}

	if err := generator.Process(context.Background(), store.Job{}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
}

func TestGeneratorCreatesACardForATextOnlyMemory(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	client, _, err := st.CreateClient(ctx, "jarvis-memory", auth.KindJarvis, "", []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	saved, err := st.SaveMemory(ctx, store.MemoryParams{
		ClientID:       client.ID,
		Source:         "jarvis",
		IdempotencyKey: "memory-generate",
		Request: memory.Request{
			ExternalID: "evt_generate",
			RawText:    "Why does migration 004 run before 003?",
		},
		Now: now,
	})
	if err != nil {
		t.Fatalf("SaveMemory() error = %v", err)
	}
	if saved.JobID == nil {
		t.Fatal("SaveMemory() returned no card job")
	}

	generator := &card.Generator{
		Store:  st,
		Client: &fakeClient{content: generatedCardJSON()},
		Model:  "test-model",
		Now:    func() time.Time { return now.Add(time.Minute) },
	}
	if err := generator.Process(ctx, queuedJob(t, st, *saved.JobID)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}

	record, err := st.GetRecord(ctx, saved.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusOK {
		t.Errorf("CardStatus = %q, want ok", record.CardStatus)
	}
	if record.Title != "Fix SQLite migration ordering" {
		t.Errorf("Title = %q, want the generated card title", record.Title)
	}
	if record.CardModel == nil || *record.CardModel != "test-model" {
		t.Errorf("CardModel = %v, want test-model", record.CardModel)
	}

	result, err := st.Search(ctx, store.SearchParams{Query: "quicksilver", Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != saved.RecordID {
		t.Fatalf("Search(quicksilver) = %+v, want record %d", result.Items, saved.RecordID)
	}
}
