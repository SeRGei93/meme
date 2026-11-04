package store

import (
	"context"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/ingest"
)

func TestSaveCardReplacesHeuristicCardAndUpdatesFTS(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-save", "ses_card_save", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	generated := ingest.Card{
		Title:     "Generated title about quicksilver",
		Summary:   "A generated summary that replaces the heuristic fallback.",
		Questions: []string{"How was quicksilver configured?", "Why did quicksilver fail?", "Where is quicksilver stored?", "Which version of quicksilver?", "How to retry quicksilver?"},
		Keywords:  []string{"quicksilver", "generated", "card"},
		Atoms:     []string{"Quicksilver uses a file-backed cache."},
	}
	indexedAt := now.Add(time.Minute)
	applied, err := st.SaveCard(ctx, recordID, before.FinalizedAt, generated, "test-model", indexedAt)
	if err != nil {
		t.Fatalf("SaveCard() error = %v", err)
	}
	if !applied {
		t.Fatal("SaveCard() applied = false, want true")
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.Title != generated.Title {
		t.Errorf("Title = %q, want %q", after.Title, generated.Title)
	}
	if after.Summary != generated.Summary {
		t.Errorf("Summary = %q, want %q", after.Summary, generated.Summary)
	}
	if len(after.Questions) != len(generated.Questions) {
		t.Errorf("Questions = %v, want %v", after.Questions, generated.Questions)
	}
	if after.CardStatus != CardStatusOK {
		t.Errorf("CardStatus = %q, want ok", after.CardStatus)
	}
	if after.CardModel == nil || *after.CardModel != "test-model" {
		t.Errorf("CardModel = %v, want test-model", after.CardModel)
	}
	if !after.IndexedAt.Equal(indexedAt) {
		t.Errorf("IndexedAt = %s, want %s", after.IndexedAt, indexedAt)
	}
	if after.Revision != before.Revision {
		t.Errorf("Revision = %d, want %d (a card is not a content revision)", after.Revision, before.Revision)
	}

	result, err := st.Search(ctx, SearchParams{Query: "quicksilver", Now: indexedAt})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != recordID {
		t.Fatalf("Search(quicksilver) = %+v, want record %d", result.Items, recordID)
	}
}

func TestSaveCardIgnoresAStaleFinalization(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-stale", "ses_card_stale", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if before.FinalizedAt == nil {
		t.Fatal("final record has no finalized_at")
	}

	generated := ingest.Card{
		Title:     "Stale title",
		Summary:   "Stale summary.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"stale"},
		Atoms:     []string{"stale atom"},
	}
	newerFinalization := before.FinalizedAt.Add(time.Minute)
	applied, err := st.SaveCard(ctx, recordID, &newerFinalization, generated, "test-model", now)
	if err != nil {
		t.Fatalf("SaveCard() error = %v", err)
	}
	if applied {
		t.Fatal("SaveCard() applied = true for a stale finalization")
	}

	applied, err = st.SaveCard(ctx, recordID, before.FinalizedAt, generated, "test-model", now)
	if err != nil {
		t.Fatalf("SaveCard(current finalization) error = %v", err)
	}
	if !applied {
		t.Fatal("SaveCard(current finalization) applied = false, want true")
	}
}

func TestSaveCardStillAppliesAfterALiveCapture(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-live", "ses_card_live", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if before.FinalizedAt == nil {
		t.Fatal("final record has no finalized_at")
	}

	// A live capture updates raw_text and revision but must not invalidate the
	// card job that is already running for this finalized version.
	live := ingest.Bundle{
		BundleID:   "2f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID: "ses_card_live",
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session ses_card_live",
		Final:      false,
		StartedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(10 * time.Minute),
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "Question ses_card_live", At: now.Add(-time.Hour)},
			{ID: "msg_2", Role: ingest.RoleAssistant, Text: "A later live answer", At: now.Add(10 * time.Minute)},
		},
	}
	if _, err := st.IngestSession(ctx, IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: live.BundleID,
		Bundle:         live,
		Now:            now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("live IngestSession() error = %v", err)
	}

	afterLive, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if afterLive.Revision == before.Revision {
		t.Fatal("live capture did not bump the revision")
	}
	if afterLive.FinalizedAt == nil || !afterLive.FinalizedAt.Equal(*before.FinalizedAt) {
		t.Fatalf("live capture changed finalized_at: %v -> %v", before.FinalizedAt, afterLive.FinalizedAt)
	}

	generated := ingest.Card{
		Title:     "Generated after a live capture",
		Summary:   "The card still applies.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"applied"},
		Atoms:     []string{"The card applies to the finalized version."},
	}
	applied, err := st.SaveCard(ctx, recordID, before.FinalizedAt, generated, "test-model", now.Add(11*time.Minute))
	if err != nil {
		t.Fatalf("SaveCard() error = %v", err)
	}
	if !applied {
		t.Fatal("SaveCard() applied = false after a live capture, want true")
	}
}

func TestSaveCardLetsANewerFinalVersionWriteItsOwnCard(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-version-a", "ses_card_version", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if before.FinalizedAt == nil {
		t.Fatal("final record has no finalized_at")
	}

	newer := ingest.Bundle{
		BundleID:   "3f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID: "ses_card_version",
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session ses_card_version",
		Final:      true,
		StartedAt:  now.Add(-time.Hour),
		UpdatedAt:  now.Add(10 * time.Minute),
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "A newer final question", At: now.Add(10 * time.Minute)},
		},
	}
	result, err := st.IngestSession(ctx, IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: newer.BundleID,
		Bundle:         newer,
		Now:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("newer IngestSession() error = %v", err)
	}
	if result.RecordID != recordID {
		t.Fatalf("newer ingest created record %d, want %d", result.RecordID, recordID)
	}

	afterNewer, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if afterNewer.FinalizedAt == nil || afterNewer.FinalizedAt.Equal(*before.FinalizedAt) {
		t.Fatalf("newer final version did not change finalized_at: %v", afterNewer.FinalizedAt)
	}

	oldCard := ingest.Card{
		Title:     "Old card",
		Summary:   "Generated for the old final version.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"old"},
		Atoms:     []string{"old atom"},
	}
	applied, err := st.SaveCard(ctx, recordID, before.FinalizedAt, oldCard, "test-model", now)
	if err != nil {
		t.Fatalf("SaveCard(old) error = %v", err)
	}
	if applied {
		t.Fatal("old card was applied after a newer final version arrived")
	}

	newCard := ingest.Card{
		Title:     "New card",
		Summary:   "Generated for the new final version.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"new"},
		Atoms:     []string{"new atom"},
	}
	applied, err = st.SaveCard(ctx, recordID, afterNewer.FinalizedAt, newCard, "test-model", now.Add(11*time.Minute))
	if err != nil {
		t.Fatalf("SaveCard(new) error = %v", err)
	}
	if !applied {
		t.Fatal("new card was not applied for the current final version")
	}

	final, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if final.Title != "New card" || final.CardStatus != CardStatusOK {
		t.Errorf("final record = (title %q, card_status %q), want the new card", final.Title, final.CardStatus)
	}
}

func TestFailJobDoesNotFailANewerCardVersion(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

	first := ingest.Bundle{
		BundleID:   "4f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID: "ses_card_fail_version",
		ProjectKey: "github.com/SeRGei93/meme",
		Title:      "Session ses_card_fail_version",
		Final:      true,
		StartedAt:  now.Add(-time.Hour),
		UpdatedAt:  now,
		Messages: []ingest.Message{
			{ID: "msg_1", Role: ingest.RoleUser, Text: "First final question", At: now},
		},
	}
	firstResult, err := st.IngestSession(ctx, IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: first.BundleID,
		Bundle:         first,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("first IngestSession() error = %v", err)
	}
	if firstResult.JobID == nil {
		t.Fatal("first ingest returned no job")
	}

	newer := first
	newer.BundleID = "5f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3"
	newer.UpdatedAt = now.Add(10 * time.Minute)
	newer.Messages = []ingest.Message{
		{ID: "msg_2", Role: ingest.RoleUser, Text: "A newer final question", At: now.Add(10 * time.Minute)},
	}
	newerResult, err := st.IngestSession(ctx, IngestParams{
		ClientID:       clientID,
		Source:         "opencode",
		IdempotencyKey: newer.BundleID,
		Bundle:         newer,
		Now:            now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("newer IngestSession() error = %v", err)
	}

	claimed, ok, err := st.ClaimNextJob(ctx, now)
	if err != nil {
		t.Fatalf("ClaimNextJob() error = %v", err)
	}
	if !ok || claimed.ID != *firstResult.JobID {
		t.Fatalf("ClaimNextJob() = (%+v, %v), want the older job %d", claimed, ok, *firstResult.JobID)
	}
	if err := st.FailJob(ctx, claimed.ID, now.Add(11*time.Minute), "llm is down"); err != nil {
		t.Fatalf("FailJob() error = %v", err)
	}

	current, err := st.GetRecord(ctx, firstResult.RecordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if current.CardStatus != CardStatusQueued {
		t.Errorf("CardStatus after the old job failed = %q, want queued for the newer version", current.CardStatus)
	}

	generated := ingest.Card{
		Title:     "Card for the newer version",
		Summary:   "Generated from the newer final content.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"newer"},
		Atoms:     []string{"newer atom"},
	}
	applied, err := st.SaveCard(ctx, newerResult.RecordID, current.FinalizedAt, generated, "test-model", now.Add(12*time.Minute))
	if err != nil {
		t.Fatalf("SaveCard() error = %v", err)
	}
	if !applied {
		t.Fatal("SaveCard() for the newer version was discarded after the old job failed")
	}
}

func TestFailCardKeepsTheHeuristicCard(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-fail", "ses_card_fail", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	applied, err := st.FailCard(ctx, recordID, before.FinalizedAt, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("FailCard() error = %v", err)
	}
	if !applied {
		t.Fatal("FailCard() applied = false, want true")
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.CardStatus != CardStatusFailed {
		t.Errorf("CardStatus = %q, want failed", after.CardStatus)
	}
	if after.Title != before.Title || after.Summary != before.Summary {
		t.Errorf("heuristic card changed: title %q -> %q, summary %q -> %q",
			before.Title, after.Title, before.Summary, after.Summary)
	}
	if after.Revision != before.Revision {
		t.Errorf("Revision = %d, want %d", after.Revision, before.Revision)
	}
}

func TestFailCardIgnoresAStaleFinalization(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-fail-stale", "ses_card_fail_stale", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if before.FinalizedAt == nil {
		t.Fatal("final record has no finalized_at")
	}

	newerFinalization := before.FinalizedAt.Add(time.Minute)
	applied, err := st.FailCard(ctx, recordID, &newerFinalization, now)
	if err != nil {
		t.Fatalf("FailCard() error = %v", err)
	}
	if applied {
		t.Fatal("FailCard() applied = true for a stale finalization")
	}
}

func TestKeepHeuristicCardMarksTheCardTerminal(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-heuristic", "ses_card_heuristic", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	applied, err := st.KeepHeuristicCard(ctx, recordID, before.FinalizedAt, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("KeepHeuristicCard() error = %v", err)
	}
	if !applied {
		t.Fatal("KeepHeuristicCard() applied = false, want true")
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.CardStatus != CardStatusHeuristic {
		t.Errorf("CardStatus = %q, want heuristic", after.CardStatus)
	}
	if after.Title != before.Title {
		t.Errorf("Title = %q, want heuristic title %q", after.Title, before.Title)
	}
}
