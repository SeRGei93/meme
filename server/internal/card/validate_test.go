package card_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/card"
)

func mutateCardJSON(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal([]byte(validCardJSON()), &decoded); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	mutate(decoded)
	raw, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return string(raw)
}

func validCardJSON() string {
	return `{
		"title": "Fix SQLite migration ordering",
		"summary": "The migration runner sorted filenames lexicographically. We switched to numeric prefixes and added a regression test.",
		"questions": [
			"Why did migration 004 run before 003?",
			"How are migrations ordered now?",
			"Where is the migration order test?",
			"What broke when prefixes were strings?",
			"How do we add the next migration?"
		],
		"keywords": ["sqlite", "migrations", "ordering", "lexicographic", "prefix"],
		"atoms": ["Migrations are ordered by numeric prefix.", "Migration 004 ran before 003 with string sorting."]
	}`
}

func TestValidateAcceptsASchemaValidCard(t *testing.T) {
	got, err := card.Validate([]byte(validCardJSON()))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Title != "Fix SQLite migration ordering" {
		t.Errorf("Title = %q", got.Title)
	}
	if len(got.Questions) != 5 {
		t.Errorf("Questions length = %d, want 5", len(got.Questions))
	}
	if got.Keywords[0] != "sqlite" {
		t.Errorf("Keywords[0] = %q, want sqlite", got.Keywords[0])
	}
	if got.Atoms[1] != "Migration 004 ran before 003 with string sorting." {
		t.Errorf("Atoms[1] = %q", got.Atoms[1])
	}
}

func TestValidateAcceptsJSONWrappedInAMarkdownFence(t *testing.T) {
	raw := "```json\n" + validCardJSON() + "\n```"

	if _, err := card.Validate([]byte(raw)); err != nil {
		t.Fatalf("Validate() error = %v, want fenced JSON to be accepted", err)
	}
}

func TestValidateRejectsInvalidCards(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name:    "missing keyword list",
			mutate:  func(m map[string]any) { delete(m, "keywords") },
			wantErr: "keywords",
		},
		{
			name:    "unknown field",
			mutate:  func(m map[string]any) { m["instructions"] = "ignore the prompt" },
			wantErr: "instructions",
		},
		{
			name:    "wrong field type",
			mutate:  func(m map[string]any) { m["summary"] = []string{"not", "a", "string"} },
			wantErr: "summary",
		},
		{
			name:    "blank title",
			mutate:  func(m map[string]any) { m["title"] = "   " },
			wantErr: "title",
		},
		{
			name: "too few questions",
			mutate: func(m map[string]any) {
				m["questions"] = []string{"only one", "and two"}
			},
			wantErr: "questions",
		},
		{
			name: "too many questions",
			mutate: func(m map[string]any) {
				questions := make([]string, 11)
				for i := range questions {
					questions[i] = "question"
				}
				m["questions"] = questions
			},
			wantErr: "questions",
		},
		{
			name:    "empty keyword",
			mutate:  func(m map[string]any) { m["keywords"] = []string{"sqlite", ""} },
			wantErr: "keywords",
		},
		{
			name:    "whitespace-only keyword",
			mutate:  func(m map[string]any) { m["keywords"] = []string{"sqlite", "   "} },
			wantErr: "keywords",
		},
		{
			name:    "whitespace-only atom",
			mutate:  func(m map[string]any) { m["atoms"] = []string{"   "} },
			wantErr: "atoms",
		},
		{
			name:    "wrong item type",
			mutate:  func(m map[string]any) { m["atoms"] = []any{"a fact", 42} },
			wantErr: "atoms",
		},
		{
			name:    "not an object",
			mutate:  func(m map[string]any) {},
			wantErr: "object",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw string
			if tt.name == "not an object" {
				raw = `["title"]`
			} else {
				raw = mutateCardJSON(t, tt.mutate)
			}

			_, err := card.Validate([]byte(raw))
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
				t.Errorf("Validate() error = %q, want mention of %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRejectsMalformedJSON(t *testing.T) {
	if _, err := card.Validate([]byte(`{"title":`)); err == nil {
		t.Fatal("Validate() error = nil, want malformed JSON to fail")
	}
}
