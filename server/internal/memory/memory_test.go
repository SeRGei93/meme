package memory_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/memory"
)

func decodeRequest(t *testing.T, body string) memory.Request {
	t.Helper()

	request, err := memory.Decode(strings.NewReader(body))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return request
}

func validationField(t *testing.T, err error) string {
	t.Helper()

	var validationError *memory.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want *memory.ValidationError", err)
	}
	return validationError.Field
}

func TestDecodeAndValidateAcceptAProvidedCard(t *testing.T) {
	request := decodeRequest(t, `{
		"external_id": "evt_01JABCDEF",
		"project_key": "second-brain",
		"occurred_at": "2026-11-03T13:30:00Z",
		"title": "Choose SQLite FTS5 over pgvector",
		"summary": "v1 uses SQLite FTS5 with BM25 and freshness decay.",
		"questions": ["Why not pgvector?"],
		"keywords": ["sqlite", "fts5", "decision"],
		"atoms": ["No vector search in v1."],
		"raw_text": "Decision: use SQLite FTS5 instead of pgvector.",
		"metadata": {"agent": "jarvis"}
	}`)

	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !request.HasCard() {
		t.Error("HasCard() = false, want true for a title and summary")
	}
	if request.ExternalID != "evt_01JABCDEF" {
		t.Errorf("ExternalID = %q", request.ExternalID)
	}
	if request.ProjectKey != "second-brain" {
		t.Errorf("ProjectKey = %q", request.ProjectKey)
	}
	if request.OccurredAt == nil || !request.OccurredAt.Equal(time.Date(2026, 11, 3, 13, 30, 0, 0, time.UTC)) {
		t.Errorf("OccurredAt = %v, want 2026-11-03T13:30:00Z", request.OccurredAt)
	}
	if len(request.Questions) != 1 || request.Questions[0] != "Why not pgvector?" {
		t.Errorf("Questions = %v", request.Questions)
	}
	if len(request.Keywords) != 3 || request.Keywords[0] != "sqlite" {
		t.Errorf("Keywords = %v", request.Keywords)
	}
	if len(request.Atoms) != 1 || request.Atoms[0] != "No vector search in v1." {
		t.Errorf("Atoms = %v", request.Atoms)
	}
	if request.Metadata["agent"] != "jarvis" {
		t.Errorf("Metadata = %v", request.Metadata)
	}
}

func TestDecodeAcceptsTextOnlyMemory(t *testing.T) {
	request := decodeRequest(t, `{"raw_text": "Remind me that the backup bucket is called meme."}`)

	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if request.HasCard() {
		t.Error("HasCard() = true, want false without a title and summary")
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	_, err := memory.Decode(strings.NewReader(`{"raw_text": "x", "source": "jarvis"}`))
	if err == nil {
		t.Fatal("Decode() error = nil, want an unknown-field error")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("Decode() error = %v, want it to name the unknown field", err)
	}
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	longSummary := strings.Repeat("я", 5001)
	emptyQuestion := []string{"ok", ""}

	tests := []struct {
		name    string
		request memory.Request
		field   string
	}{
		{"missing raw text", memory.Request{}, "raw_text"},
		{"empty raw text", memory.Request{RawText: ""}, "raw_text"},
		{"raw text too long", memory.Request{RawText: strings.Repeat("я", 2_000_001)}, "raw_text"},
		{"external id too long", memory.Request{RawText: "x", ExternalID: strings.Repeat("e", 201)}, "external_id"},
		{"project key too long", memory.Request{RawText: "x", ProjectKey: strings.Repeat("p", 501)}, "project_key"},
		{"summary too long", memory.Request{RawText: "x", Summary: longSummary}, "summary"},
		{"too many questions", memory.Request{RawText: "x", Questions: make([]string, 51)}, "questions"},
		{"empty question", memory.Request{RawText: "x", Questions: emptyQuestion}, "questions[1]"},
		{"too many keywords", memory.Request{RawText: "x", Keywords: make([]string, 101)}, "keywords"},
		{"keyword too long", memory.Request{RawText: "x", Keywords: []string{strings.Repeat("k", 201)}}, "keywords[0]"},
		{"too many atoms", memory.Request{RawText: "x", Atoms: make([]string, 101)}, "atoms"},
		{"atom too long", memory.Request{RawText: "x", Atoms: []string{strings.Repeat("a", 1001)}}, "atoms[0]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validationField(t, tt.request.Validate()); got != tt.field {
				t.Errorf("field = %q, want %q", got, tt.field)
			}
		})
	}
}

func TestValidateRejectsOverlongTitle(t *testing.T) {
	request := memory.Request{RawText: "x", Title: strings.Repeat("t", 501)}
	if got := validationField(t, request.Validate()); got != "title" {
		t.Errorf("field = %q, want title", got)
	}
}

func TestMaskedRemovesSecretsFromEveryContentField(t *testing.T) {
	request := rememberSecrets()

	masked := request.Masked()

	if containsSecret(masked.RawText) {
		t.Errorf("masked raw_text still contains a secret: %q", masked.RawText)
	}
	if !strings.Contains(masked.RawText, "[REDACTED]") {
		t.Errorf("masked raw_text = %q, want the redaction marker", masked.RawText)
	}
	for _, field := range []string{masked.Title, masked.Summary} {
		if containsSecret(field) {
			t.Errorf("masked card field still contains a secret: %q", field)
		}
	}
	for _, list := range [][]string{masked.Questions, masked.Keywords, masked.Atoms} {
		for _, value := range list {
			if containsSecret(value) {
				t.Errorf("masked list value still contains a secret: %q", value)
			}
		}
	}
	if containsSecret(masked.Metadata["note"].(string)) {
		t.Errorf("masked metadata still contains a secret: %v", masked.Metadata)
	}
	if containsSecret(masked.Metadata["nested"].(map[string]any)["token"].(string)) {
		t.Errorf("masked nested metadata still contains a secret: %v", masked.Metadata)
	}
}

func TestMaskedKeepsNonSecretContent(t *testing.T) {
	request := memory.Request{
		RawText: "The backup bucket is called meme.",
		Title:   "Backup bucket",
	}

	masked := request.Masked()

	if masked.RawText != request.RawText {
		t.Errorf("masked raw_text = %q, want %q", masked.RawText, request.RawText)
	}
	if masked.Title != request.Title {
		t.Errorf("masked title = %q, want %q", masked.Title, request.Title)
	}
}

func TestContentHashIsStableForIdenticalContent(t *testing.T) {
	first := memory.Request{RawText: "Decision: use FTS5.", ExternalID: "evt_1", ProjectKey: "second-brain"}
	second := memory.Request{RawText: "Decision: use FTS5.", ExternalID: "evt_2", ProjectKey: "second-brain"}

	if first.ContentHash() != second.ContentHash() {
		t.Error("ContentHash() differs for the same content with a different external_id")
	}

	changed := second
	changed.RawText = "Decision: use pgvector."
	if first.ContentHash() == changed.ContentHash() {
		t.Error("ContentHash() matches after the content changed")
	}
}

func TestContentHashIgnoresSecretValues(t *testing.T) {
	first := memory.Request{RawText: "set password=hunter2 for the bucket"}
	second := memory.Request{RawText: "set password=correct-horse for the bucket"}

	if first.ContentHash() != second.ContentHash() {
		t.Error("ContentHash() differs for captures that only differ in a secret value")
	}
}

func TestContentHashTreatsOmittedProjectKeyAsTheDefault(t *testing.T) {
	omitted := memory.Request{RawText: "x"}
	explicit := memory.Request{RawText: "x", ProjectKey: "jarvis"}

	if omitted.ContentHash() != explicit.ContentHash() {
		t.Error("ContentHash() differs between an omitted and an explicit default project key")
	}
}

func TestExternalIDOrAutoIsStable(t *testing.T) {
	request := memory.Request{RawText: "Remind me that the backup bucket is called meme."}

	generated := request.ExternalIDOrAuto()
	if !strings.HasPrefix(generated, "auto_") {
		t.Errorf("ExternalIDOrAuto() = %q, want an auto_ prefix", generated)
	}
	if len(strings.TrimPrefix(generated, "auto_")) != 32 {
		t.Errorf("ExternalIDOrAuto() = %q, want 32 hex characters after auto_", generated)
	}
	if request.ExternalIDOrAuto() != generated {
		t.Error("ExternalIDOrAuto() is not stable for identical content")
	}
	if (memory.Request{RawText: "Different text."}).ExternalIDOrAuto() == generated {
		t.Error("ExternalIDOrAuto() collides for different content")
	}
}

func TestExternalIDOrAutoKeepsAnExplicitID(t *testing.T) {
	request := memory.Request{RawText: "x", ExternalID: "evt_01JABCDEF"}
	if got := request.ExternalIDOrAuto(); got != "evt_01JABCDEF" {
		t.Errorf("ExternalIDOrAuto() = %q, want the explicit external_id", got)
	}
}

func TestEffectiveProjectKeyDefaultsToJarvis(t *testing.T) {
	request := memory.Request{RawText: "x"}
	if got := request.EffectiveProjectKey(); got != "jarvis" {
		t.Errorf("EffectiveProjectKey() = %q, want jarvis", got)
	}

	explicit := memory.Request{RawText: "x", ProjectKey: "second-brain"}
	if got := explicit.EffectiveProjectKey(); got != "second-brain" {
		t.Errorf("EffectiveProjectKey() = %q, want second-brain", got)
	}
}

func TestRequestHashDistinguishesExternalIDs(t *testing.T) {
	first := memory.Request{RawText: "x", ExternalID: "evt_1"}
	second := memory.Request{RawText: "x", ExternalID: "evt_2"}

	if first.RequestHash() == second.RequestHash() {
		t.Error("RequestHash() matches for different external_ids")
	}
}

func rememberSecrets() memory.Request {
	return memory.Request{
		ExternalID: "evt_secret",
		ProjectKey: "jarvis",
		Title:      "Rotate password=hunter2",
		Summary:    "Use token=ghp_abcdefghijklmnopqrstuvwxyz0123456789 next.",
		Questions:  []string{"Where is password=hunter2 stored?"},
		Keywords:   []string{"token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		Atoms:      []string{"secret=hunter2"},
		RawText:    "The old secret was password=hunter2 and token=ghp_abcdefghijklmnopqrstuvwxyz0123456789.",
		Metadata: map[string]any{
			"note":   "password=hunter2",
			"nested": map[string]any{"token": "secret=hunter2"},
		},
	}
}

func containsSecret(value string) bool {
	return strings.Contains(value, "hunter2") ||
		strings.Contains(value, "ghp_abcdefghijklmnopqrstuvwxyz0123456789")
}
