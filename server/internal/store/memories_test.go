package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/memory"
	"github.com/SeRGei93/meme/server/internal/store"
)

func openMemoryStore(t *testing.T) (*store.Store, int64) {
	t.Helper()

	st, _ := openTestStore(t)
	client, _, err := st.CreateClient(context.Background(), "jarvis-writer", auth.KindJarvis, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	return st, client.ID
}

func saveMemory(t *testing.T, st *store.Store, clientID int64, key string, request memory.Request, now time.Time) store.MemoryResult {
	t.Helper()

	result, err := st.SaveMemory(context.Background(), store.MemoryParams{
		ClientID:       clientID,
		Source:         auth.SourceJarvis,
		IdempotencyKey: key,
		Request:        request,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("SaveMemory() error = %v", err)
	}
	return result
}

func memoryCard() memory.Request {
	return memory.Request{
		ExternalID: "evt_01JABCDEF",
		ProjectKey: "second-brain",
		OccurredAt: ptrTime(time.Date(2026, 11, 3, 13, 30, 0, 0, time.UTC)),
		Title:      "Choose SQLite FTS5 over pgvector",
		Summary:    "v1 uses SQLite FTS5 with BM25 and freshness decay.",
		Questions:  []string{"Why not pgvector?"},
		Keywords:   []string{"sqlite", "fts5", "decision"},
		Atoms:      []string{"No vector search in v1."},
		RawText:    "Decision: use SQLite FTS5 instead of pgvector.",
		Metadata:   map[string]any{"agent": "jarvis"},
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func TestSaveMemoryWithAProvidedCardSkipsTheQueue(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	result := saveMemory(t, st, clientID, "memory-1", memoryCard(), now)

	if result.Replayed {
		t.Error("Replayed = true, want false on the first save")
	}
	if result.Status != store.MemoryStatusIndexed {
		t.Errorf("Status = %q, want indexed", result.Status)
	}
	if result.JobID != nil {
		t.Errorf("JobID = %v, want nil when the card is provided", result.JobID)
	}

	record, err := st.GetRecord(context.Background(), result.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.Kind != "memory" {
		t.Errorf("Kind = %q, want memory", record.Kind)
	}
	if record.Source != auth.SourceJarvis {
		t.Errorf("Source = %q, want jarvis", record.Source)
	}
	if record.ProjectKey != "second-brain" {
		t.Errorf("ProjectKey = %q, want second-brain", record.ProjectKey)
	}
	if record.Title != "Choose SQLite FTS5 over pgvector" {
		t.Errorf("Title = %q, want the provided title", record.Title)
	}
	if record.Summary != "v1 uses SQLite FTS5 with BM25 and freshness decay." {
		t.Errorf("Summary = %q, want the provided summary", record.Summary)
	}
	if record.CardStatus != store.CardStatusOK {
		t.Errorf("CardStatus = %q, want ok", record.CardStatus)
	}
	if !record.IsFinal {
		t.Error("IsFinal = false, want true for a memory")
	}
	if record.FinalizedAt == nil {
		t.Error("FinalizedAt = nil, want a timestamp")
	}
	if record.StartedAt == nil || !record.StartedAt.Equal(*memoryCard().OccurredAt) {
		t.Errorf("StartedAt = %v, want the occurred_at value", record.StartedAt)
	}
	if record.Revision != 1 {
		t.Errorf("Revision = %d, want 1", record.Revision)
	}
	if record.Metadata["agent"] != "jarvis" {
		t.Errorf("Metadata = %v, want the request metadata", record.Metadata)
	}

	jobs, err := st.ListJobs(context.Background(), store.ListJobsParams{})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs.Items) != 0 {
		t.Fatalf("jobs = %+v, want no card job for a provided card", jobs.Items)
	}

	found, err := st.Search(context.Background(), store.SearchParams{Query: "pgvector", Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found.Items) != 1 || found.Items[0].ID != result.RecordID {
		t.Errorf("Search(pgvector) = %+v, want record %d", found.Items, result.RecordID)
	}
}

func TestSaveMemoryTextOnlyQueuesCardGeneration(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	request := memory.Request{
		RawText: "Remind me that the backup bucket is called meme.",
	}
	result := saveMemory(t, st, clientID, "memory-2", request, now)

	if result.Status != store.MemoryStatusQueued {
		t.Errorf("Status = %q, want queued", result.Status)
	}
	if result.JobID == nil {
		t.Fatal("JobID = nil, want a card-generation job")
	}

	job, err := st.GetJob(context.Background(), *result.JobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Kind != "memory" {
		t.Errorf("job.Kind = %q, want memory", job.Kind)
	}
	if job.Status != store.JobQueued {
		t.Errorf("job.Status = %q, want queued", job.Status)
	}
	if job.RecordID == nil || *job.RecordID != result.RecordID {
		t.Errorf("job.RecordID = %v, want %d", job.RecordID, result.RecordID)
	}
	if !strings.HasPrefix(job.ExternalID, "auto_") {
		t.Errorf("job.ExternalID = %q, want the content-derived external id", job.ExternalID)
	}

	record, err := st.GetRecord(context.Background(), result.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.CardStatus != store.CardStatusQueued {
		t.Errorf("CardStatus = %q, want queued", record.CardStatus)
	}
	if record.Title != "Remind me that the backup bucket is called meme." {
		t.Errorf("Title = %q, want the first line as a heuristic title", record.Title)
	}
	if record.ProjectKey != "jarvis" {
		t.Errorf("ProjectKey = %q, want the default jarvis", record.ProjectKey)
	}
	if !record.IsFinal {
		t.Error("IsFinal = false, want true for a memory")
	}

	found, err := st.Search(context.Background(), store.SearchParams{Query: "backup", Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found.Items) != 1 || found.Items[0].ID != result.RecordID {
		t.Errorf("Search(backup) = %+v, want record %d", found.Items, result.RecordID)
	}
}

func TestSaveMemoryIsIdempotentForTheSameContent(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	first := saveMemory(t, st, clientID, "memory-3", memoryCard(), now)
	second := saveMemory(t, st, clientID, "memory-3", memoryCard(), now.Add(time.Minute))
	if !second.Replayed {
		t.Error("Replayed = false, want true for the same Idempotency-Key and body")
	}
	if second.RecordID != first.RecordID {
		t.Errorf("RecordID = %d, want the original %d", second.RecordID, first.RecordID)
	}

	third := saveMemory(t, st, clientID, "memory-3-other-key", memoryCard(), now.Add(2*time.Minute))
	if !third.Replayed {
		t.Error("Replayed = false, want true for the same external_id and content")
	}
	if third.RecordID != first.RecordID {
		t.Errorf("RecordID = %d, want the original %d", third.RecordID, first.RecordID)
	}

	record, err := st.GetRecord(context.Background(), first.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.Revision != 1 {
		t.Errorf("Revision = %d, want 1 after replays", record.Revision)
	}
}

func TestSaveMemoryRejectsChangedContentForTheSameExternalID(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	first := saveMemory(t, st, clientID, "memory-4", memoryCard(), now)

	changed := memoryCard()
	changed.RawText = "Decision: use pgvector instead."
	_, err := st.SaveMemory(context.Background(), store.MemoryParams{
		ClientID:       clientID,
		Source:         auth.SourceJarvis,
		IdempotencyKey: "memory-4-changed",
		Request:        changed,
		Now:            now.Add(time.Minute),
	})
	if !errors.Is(err, store.ErrMemoryConflict) {
		t.Fatalf("SaveMemory() error = %v, want ErrMemoryConflict", err)
	}

	record, err := st.GetRecord(context.Background(), first.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.RawText != memoryCard().RawText {
		t.Errorf("RawText = %q, want the original content", record.RawText)
	}
}

func TestSaveMemoryRejectsAReusedIdempotencyKeyWithADifferentBody(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	saveMemory(t, st, clientID, "memory-5", memoryCard(), now)

	_, err := st.SaveMemory(context.Background(), store.MemoryParams{
		ClientID:       clientID,
		Source:         auth.SourceJarvis,
		IdempotencyKey: "memory-5",
		Request:        memory.Request{ExternalID: "evt_other", RawText: "A different memory."},
		Now:            now.Add(time.Minute),
	})
	if !errors.Is(err, store.ErrIdempotencyKeyReused) {
		t.Fatalf("SaveMemory() error = %v, want ErrIdempotencyKeyReused", err)
	}
}

func TestSaveMemoryMasksSecretsBeforePersistence(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	request := memory.Request{
		ExternalID: "evt_secret",
		Title:      "Rotate password=hunter2",
		Summary:    "Use token=ghp_abcdefghijklmnopqrstuvwxyz0123456789.",
		RawText:    "The old secret was password=hunter2.",
		Metadata:   map[string]any{"note": "password=hunter2"},
	}
	result := saveMemory(t, st, clientID, "memory-6", request, now)

	record, err := st.GetRecord(context.Background(), result.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	for name, value := range map[string]string{
		"title":    record.Title,
		"summary":  record.Summary,
		"raw_text": record.RawText,
	} {
		if strings.Contains(value, "hunter2") || strings.Contains(value, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
			t.Errorf("%s still contains a secret: %q", name, value)
		}
	}
	if note, _ := record.Metadata["note"].(string); strings.Contains(note, "hunter2") {
		t.Errorf("metadata still contains a secret: %v", record.Metadata)
	}
}

func TestSaveMemoryFallsBackToTheExternalIDWhenTheFirstLineIsBlank(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	result := saveMemory(t, st, clientID, "memory-blank", memory.Request{
		ExternalID: "evt_blank",
		RawText:    "\n\n",
	}, now)

	record, err := st.GetRecord(context.Background(), result.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if record.Title != "evt_blank" {
		t.Errorf("Title = %q, want the external id as a fallback title", record.Title)
	}
}

func TestSaveMemoryDerivesExternalIDFromContentWhenOmitted(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	request := memory.Request{RawText: "The backup bucket is called meme."}
	first := saveMemory(t, st, clientID, "auto-1", request, now)
	second := saveMemory(t, st, clientID, "auto-2", request, now.Add(time.Minute))
	if !second.Replayed || second.RecordID != first.RecordID {
		t.Fatalf("second save = %+v, want a replay of record %d", second, first.RecordID)
	}

	other := memory.Request{RawText: "The backup schedule is every six hours."}
	third := saveMemory(t, st, clientID, "auto-3", other, now.Add(2*time.Minute))
	if third.RecordID == first.RecordID {
		t.Error("a different auto-derived memory reused the first record")
	}

	found, err := st.Search(context.Background(), store.SearchParams{Query: "backup", Now: now})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(found.Items) != 2 {
		t.Errorf("Search(backup) returned %d items, want 2 distinct memories", len(found.Items))
	}
}

func TestSaveMemoryKeepsTheSameMemoryForAnotherClient(t *testing.T) {
	st, clientID := openMemoryStore(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	opencode, _, err := st.CreateClient(context.Background(), "opencode-writer", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	jarvisResult := saveMemory(t, st, clientID, "memory-7", memoryCard(), now)
	opencodeResult := saveMemory(t, st, opencode.ID, "memory-7", memoryCard(), now)

	if opencodeResult.RecordID == jarvisResult.RecordID {
		t.Error("the same external_id from different clients reused one record")
	}
	if opencodeResult.Replayed {
		t.Error("Replayed = true, want false for a different client")
	}
}
