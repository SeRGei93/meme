package search_test

import (
	"errors"
	"testing"

	"github.com/SeRGei93/meme/server/internal/search"
)

func TestMatchQuery(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"latin stays exact", "SQLite", `"sqlite"`},
		{"multiple terms", "база данных", `"баз"* "данн"*`},
		{"russian noun ending", "Миграцией", `"миграц"*`},
		{"russian adjective ending", "новым", `"нов"*`},
		{"russian participle ending", "данным", `"данн"*`},
		{"short root keeps soft stem", "музей", `"музе"*`},
		{"short adjective keeps root", "новый", `"нов"*`},
		{"single quoted phrase", `"база данных"`, `"база данных"`},
		{"two quoted terms are two phrases", `"база" "данных"`, `"база" "данных"`},
		{"mixed phrase keeps its words together", `sqlite "fix ordering"`, `"sqlite" "fix ordering"`},
		{"unbalanced quote falls back to terms", `sqlite "fix ordering`, `"sqlite" "fix" "ordering"`},
		{"punctuation is tokenized", "C++", `"c"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := search.MatchQuery(tt.raw)
			if err != nil {
				t.Fatalf("MatchQuery(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("MatchQuery(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestMatchQueryRejectsEmptyInput(t *testing.T) {
	for _, raw := range []string{"", "   ", "!!!", `""`} {
		if _, err := search.MatchQuery(raw); !errors.Is(err, search.ErrEmptyQuery) {
			t.Errorf("MatchQuery(%q) error = %v, want ErrEmptyQuery", raw, err)
		}
	}
}

func TestRankingConstantsMatchArchitecture(t *testing.T) {
	if search.BM25Weights != "6, 4, 4, 5, 3, 1" {
		t.Errorf("BM25Weights = %q, want title/summary/questions/keywords/atoms/raw_text = 6/4/4/5/3/1", search.BM25Weights)
	}
	if search.FreshnessHalfLifeDays != 120 {
		t.Errorf("FreshnessHalfLifeDays = %v, want 120", search.FreshnessHalfLifeDays)
	}
}
