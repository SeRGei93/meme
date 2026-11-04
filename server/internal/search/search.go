// Package search owns the FTS5 query rules for memory search: normalization,
// ranking weights and the freshness half-life. Persistence and SQL stay in
// store.
package search

import (
	"errors"
	"strings"
	"unicode"
)

var ErrEmptyQuery = errors.New("query has no searchable terms")

const (
	// DefaultLimit is the contract's top-N when the caller does not pass limit.
	DefaultLimit = 8
	// MaxLimit is the contract's upper bound for GET /api/v1/search?limit.
	MaxLimit = 50
	// BM25Weights are the FTS5 column weights from docs/ARCHITECTURE.md §8.3:
	// title, summary, questions, keywords, atoms, raw_text.
	BM25Weights = "6, 4, 4, 5, 3, 1"
	// FreshnessHalfLifeDays is the freshness decay half-life from §8.3.
	FreshnessHalfLifeDays = 120.0
)

// MatchQuery normalizes a user query into FTS5 syntax. Tokens are lower-cased,
// quoted and used as prefixes; Russian tokens are shortened to a stem so word
// forms match ("миграцией" finds "миграции"). A query that is exactly one
// double-quoted phrase is matched as an exact phrase instead.
func MatchQuery(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrEmptyQuery
	}

	if isSinglePhrase(trimmed) {
		tokens := queryTokens(trimmed[1 : len(trimmed)-1])
		if len(tokens) == 0 {
			return "", ErrEmptyQuery
		}
		return `"` + strings.Join(tokens, " ") + `"`, nil
	}

	tokens := queryTokens(trimmed)
	if len(tokens) == 0 {
		return "", ErrEmptyQuery
	}

	terms := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if hasCyrillic(token) {
			token = russianStem(token)
		}
		terms = append(terms, `"`+token+`"*`)
	}
	return strings.Join(terms, " "), nil
}

func isSinglePhrase(value string) bool {
	return len(value) >= 2 &&
		strings.HasPrefix(value, `"`) &&
		strings.HasSuffix(value, `"`) &&
		strings.Count(value, `"`) == 2
}

func queryTokens(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func hasCyrillic(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// russianSuffixes are ordered longest first so the most specific inflection is
// removed before a shorter one. Prefix matching then covers the remaining word
// forms without pulling in a full morphology dictionary.
var russianSuffixes = []string{
	"иями", "ями", "ами", "ого", "его", "ому", "ему", "ыми", "ими",
	"ией", "ья", "ая", "яя", "ое", "ее", "ые", "ие", "ый", "ий", "ой", "ей",
	"ую", "юю", "ых", "их", "ов", "ев", "ам", "ям", "ах", "ях",
	"ии", "ия", "ию", "ем", "ом",
	"а", "я", "ы", "и", "е", "о", "у", "ю", "ь",
}

// russianStem removes one inflectional ending. Most endings may leave a
// three-rune stem; noun endings that commonly belong to a short root ("ей",
// "ия", "ии", "ию") require four. "музей" therefore becomes "музе" rather
// than "муз", so it finds "музея"/"музеи" without pulling in "музыка".
func russianStem(token string) string {
	runes := []rune(token)
	for _, suffix := range russianSuffixes {
		suffixRunes := []rune(suffix)
		if len(runes) <= len(suffixRunes) || !strings.HasSuffix(token, suffix) {
			continue
		}
		stem := runes[:len(runes)-len(suffixRunes)]
		minStem := 3
		switch suffix {
		case "ей", "ия", "ии", "ию":
			minStem = 4
		}
		if len(stem) >= minStem {
			return string(stem)
		}
		if suffix == "ей" {
			// Keep the soft stem instead of the bare root: музей -> музе.
			return string(runes[:len(runes)-1])
		}
	}
	return token
}
