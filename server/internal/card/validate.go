// Package card generates record cards with an OpenAI-compatible LLM, validates
// the JSON response against the card schema and falls back to the heuristic
// card written at ingest when generation fails.
package card

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/SeRGei93/meme/server/internal/ingest"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schema.json
var schemaJSON []byte

var compiledSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("card.schema.json", bytes.NewReader(schemaJSON)); err != nil {
		return nil, fmt.Errorf("load card schema: %w", err)
	}
	schema, err := compiler.Compile("card.schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile card schema: %w", err)
	}
	return schema, nil
})

// Validate checks one raw LLM response against the card JSON schema and returns
// the normalized card. Fenced JSON is accepted because models commonly wrap
// their answer in a Markdown code block.
func Validate(raw []byte) (ingest.Card, error) {
	cleaned := stripCodeFence(strings.TrimSpace(string(raw)))

	var value any
	if err := json.Unmarshal([]byte(cleaned), &value); err != nil {
		return ingest.Card{}, fmt.Errorf("card response is not valid JSON: %w", err)
	}

	schema, err := compiledSchema()
	if err != nil {
		return ingest.Card{}, err
	}
	if err := schema.Validate(value); err != nil {
		return ingest.Card{}, fmt.Errorf("card response does not match the schema: %w", err)
	}

	var payload struct {
		Title     string   `json:"title"`
		Summary   string   `json:"summary"`
		Questions []string `json:"questions"`
		Keywords  []string `json:"keywords"`
		Atoms     []string `json:"atoms"`
	}
	if err := json.Unmarshal([]byte(cleaned), &payload); err != nil {
		return ingest.Card{}, fmt.Errorf("decode card response: %w", err)
	}

	card := ingest.Card{
		Title:     strings.TrimSpace(payload.Title),
		Summary:   strings.TrimSpace(payload.Summary),
		Questions: normalizeList(payload.Questions),
		Keywords:  normalizeList(payload.Keywords),
		Atoms:     normalizeList(payload.Atoms),
	}
	if card.Title == "" {
		return ingest.Card{}, fmt.Errorf("card title must not be blank")
	}
	if card.Summary == "" {
		return ingest.Card{}, fmt.Errorf("card summary must not be blank")
	}
	if len(card.Questions) != len(payload.Questions) {
		return ingest.Card{}, fmt.Errorf("card questions must not contain blank items")
	}
	if len(card.Keywords) != len(payload.Keywords) {
		return ingest.Card{}, fmt.Errorf("card keywords must not contain blank items")
	}
	if len(card.Atoms) != len(payload.Atoms) {
		return ingest.Card{}, fmt.Errorf("card atoms must not contain blank items")
	}
	return card, nil
}

func stripCodeFence(value string) string {
	if !strings.HasPrefix(value, "```") {
		return value
	}

	firstLine, rest, ok := strings.Cut(value, "\n")
	if !ok {
		return value
	}
	if !strings.HasPrefix(firstLine, "```") {
		return value
	}
	if !strings.HasSuffix(rest, "```") {
		return value
	}
	return strings.TrimSpace(strings.TrimSuffix(rest, "```"))
}

func normalizeList(values []string) []string {
	normalized := make([]string, len(values))
	for i, value := range values {
		normalized[i] = strings.TrimSpace(value)
	}
	return normalized
}
