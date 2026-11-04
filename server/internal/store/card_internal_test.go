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
	applied, err := st.SaveCard(ctx, recordID, before.Revision, generated, "test-model", indexedAt)
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

func TestSaveCardIgnoresAStaleRevision(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-stale", "ses_card_stale", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	generated := ingest.Card{
		Title:     "Stale title",
		Summary:   "Stale summary.",
		Questions: []string{"a?", "b?", "c?", "d?", "e?"},
		Keywords:  []string{"stale"},
		Atoms:     []string{"stale atom"},
	}
	applied, err := st.SaveCard(ctx, recordID, before.Revision+1, generated, "test-model", now)
	if err != nil {
		t.Fatalf("SaveCard() error = %v", err)
	}
	if applied {
		t.Fatal("SaveCard() applied = true for a stale revision")
	}

	after, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}
	if after.Title != before.Title || after.CardStatus != CardStatusQueued {
		t.Errorf("stale card was written: title %q, card_status %q", after.Title, after.CardStatus)
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

	applied, err := st.FailCard(ctx, recordID, before.Revision, now.Add(time.Minute))
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

func TestFailCardIgnoresAStaleRevision(t *testing.T) {
	st, clientID := openJobStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	recordID := ingestSession(t, st, clientID, "card-fail-stale", "ses_card_fail_stale", now, true)

	before, err := st.GetRecord(ctx, recordID)
	if err != nil {
		t.Fatalf("GetRecord() error = %v", err)
	}

	applied, err := st.FailCard(ctx, recordID, before.Revision+1, now)
	if err != nil {
		t.Fatalf("FailCard() error = %v", err)
	}
	if applied {
		t.Fatal("FailCard() applied = true for a stale revision")
	}
}
