// Package search owns the FTS5 query rules for memory search: normalization,
// ranking weights and the freshness half-life. Persistence and SQL stay in
// store.
package search

import (
	"errors"
	"strings"
	"unicode"
)

// ErrEmptyQuery is returned when a query contains no letters or digits.
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

// MatchQuery normalizes a user query into FTS5 syntax. Latin tokens are matched
// exactly, while Russian tokens are shortened to a stem and used as prefixes so
// word forms match ("миграцией" finds "миграции"). Text inside double quotes is
// kept as an exact phrase, so `sqlite "fix ordering"` requires the two words
// together; quoting intentionally turns stemming off. An unbalanced quote
// degrades to ordinary terms instead of dropping the query.
func MatchQuery(raw string) (string, error) {
	parts := splitQuery(strings.TrimSpace(raw))
	terms := make([]string, 0, len(parts))
	for _, part := range parts {
		tokens := queryTokens(part.text)
		if len(tokens) == 0 {
			continue
		}
		if part.phrase {
			terms = append(terms, `"`+strings.Join(tokens, " ")+`"`)
			continue
		}
		for _, token := range tokens {
			if hasCyrillic(token) {
				terms = append(terms, `"`+russianStem(token)+`"*`)
				continue
			}
			terms = append(terms, `"`+token+`"`)
		}
	}
	if len(terms) == 0 {
		return "", ErrEmptyQuery
	}
	return strings.Join(terms, " "), nil
}

type queryPart struct {
	text   string
	phrase bool
}

// splitQuery separates quoted phrases from ordinary terms. On an unbalanced
// quote it returns the whole input as plain text so the caller still searches
// every word.
func splitQuery(raw string) []queryPart {
	var (
		parts   []queryPart
		current strings.Builder
		quoted  bool
	)
	for _, r := range raw {
		if r != '"' {
			current.WriteRune(r)
			continue
		}
		if quoted {
			if strings.TrimSpace(current.String()) != "" {
				parts = append(parts, queryPart{text: current.String(), phrase: true})
			}
			current.Reset()
			quoted = false
			continue
		}
		if strings.TrimSpace(current.String()) != "" {
			parts = append(parts, queryPart{text: current.String()})
		}
		current.Reset()
		quoted = true
	}
	if quoted {
		return []queryPart{{text: raw}}
	}
	if strings.TrimSpace(current.String()) != "" {
		parts = append(parts, queryPart{text: current.String()})
	}
	return parts
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
	"ую", "юю", "ых", "их", "ым", "им", "ов", "ев", "ам", "ям", "ах", "ях",
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
