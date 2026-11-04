// Package memory owns the structured-memory request contract: decoding,
// validation, content masking, stable hashing and the external id derived from
// content.
package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/mask"
)

const (
	// DefaultProjectKey is the v1 default for a memory without a project.
	DefaultProjectKey = "jarvis"
	// MaxRawTextRunes is the contract limit for raw_text.
	MaxRawTextRunes = 2_000_000

	autoExternalIDPrefix = "auto_"
	autoExternalIDHex    = 32
)

// Request is one structured memory as accepted by POST /api/v1/memories.
// `source` is intentionally absent: the server derives it from the token.
type Request struct {
	ExternalID string         `json:"external_id,omitempty"`
	ProjectKey string         `json:"project_key,omitempty"`
	OccurredAt *time.Time     `json:"occurred_at,omitempty"`
	Title      string         `json:"title,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Questions  []string       `json:"questions,omitempty"`
	Keywords   []string       `json:"keywords,omitempty"`
	Atoms      []string       `json:"atoms,omitempty"`
	RawText    string         `json:"raw_text"`
	Metadata   map[string]any `json:"metadata,omitempty"`

	present presence
}

// presence remembers which optional minLength-1 fields were sent so an explicit
// empty value can be rejected while an omitted field stays valid.
type presence struct {
	externalID, projectKey, title bool
}

// ValidationError is a schema or business-rule violation. HTTP layers map it to
// 422 validation_failed with the field in details.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + " " + e.Message
}

func fieldError(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// Decode reads one memory request with unknown fields rejected.
func Decode(r io.Reader) (Request, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Request{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Request{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}

	request.present = presence{
		externalID: hasField(fields, "external_id"),
		projectKey: hasField(fields, "project_key"),
		title:      hasField(fields, "title"),
	}
	return request, nil
}

func hasField(fields map[string]json.RawMessage, name string) bool {
	value, ok := fields[name]
	return ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// Validate applies the v1 memory schema plus the field limits.
func (r Request) Validate() error {
	if err := validateString("raw_text", r.RawText, 1, MaxRawTextRunes); err != nil {
		return err
	}
	if r.present.externalID || r.ExternalID != "" {
		if err := validateString("external_id", r.ExternalID, 1, 200); err != nil {
			return err
		}
	}
	if r.present.projectKey || r.ProjectKey != "" {
		if err := validateString("project_key", r.ProjectKey, 1, 500); err != nil {
			return err
		}
	}
	if r.present.title || r.Title != "" {
		if err := validateString("title", r.Title, 1, 500); err != nil {
			return err
		}
	}
	if err := validateMaxRunes("summary", r.Summary, 5000); err != nil {
		return err
	}
	if err := validateList("questions", r.Questions, 50, 500); err != nil {
		return err
	}
	if err := validateList("keywords", r.Keywords, 100, 200); err != nil {
		return err
	}
	if err := validateList("atoms", r.Atoms, 100, 1000); err != nil {
		return err
	}
	return nil
}

func validateList(field string, values []string, maxItems, maxLength int) error {
	if len(values) > maxItems {
		return fieldError(field, fmt.Sprintf("must contain at most %d items", maxItems))
	}
	for i, value := range values {
		if err := validateString(fmt.Sprintf("%s[%d]", field, i), value, 1, maxLength); err != nil {
			return err
		}
	}
	return nil
}

func validateString(field, value string, min, max int) error {
	length := utf8.RuneCountInString(value)
	if length < min {
		return fieldError(field, "is required")
	}
	if length > max {
		return fieldError(field, fmt.Sprintf("must be at most %d characters", max))
	}
	return nil
}

func validateMaxRunes(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return fieldError(field, fmt.Sprintf("must be at most %d characters", max))
	}
	return nil
}

// HasCard reports whether the request already carries a complete card. A
// complete card is stored as-is and never reaches the LLM.
func (r Request) HasCard() bool {
	return r.Title != "" && r.Summary != ""
}

// EffectiveProjectKey returns the project key the request will be stored under.
func (r Request) EffectiveProjectKey() string {
	if r.ProjectKey == "" {
		return DefaultProjectKey
	}
	return r.ProjectKey
}

// ExternalIDOrAuto returns the explicit external id or one derived from the
// content. The derived id is stable for identical content, so a retry without
// an external_id resolves to the same record.
func (r Request) ExternalIDOrAuto() string {
	if r.ExternalID != "" {
		return r.ExternalID
	}
	return autoExternalIDPrefix + r.ContentHash()[:autoExternalIDHex]
}

// ContentHash identifies the knowledge content. It hashes the masked request so
// retries that differ only in a secret value resolve to the same record.
// external_id is excluded because it is the key, not the content, and because
// the automatic id is derived from this hash.
func (r Request) ContentHash() string {
	r = r.Masked()

	type content struct {
		ProjectKey string         `json:"project_key"`
		OccurredAt *time.Time     `json:"occurred_at"`
		Title      string         `json:"title"`
		Summary    string         `json:"summary"`
		Questions  []string       `json:"questions"`
		Keywords   []string       `json:"keywords"`
		Atoms      []string       `json:"atoms"`
		RawText    string         `json:"raw_text"`
		Metadata   map[string]any `json:"metadata"`
	}

	var occurredAt *time.Time
	if r.OccurredAt != nil {
		utc := r.OccurredAt.UTC()
		occurredAt = &utc
	}

	raw, err := json.Marshal(content{
		ProjectKey: r.EffectiveProjectKey(),
		OccurredAt: occurredAt,
		Title:      r.Title,
		Summary:    r.Summary,
		Questions:  nonNilList(r.Questions),
		Keywords:   nonNilList(r.Keywords),
		Atoms:      nonNilList(r.Atoms),
		RawText:    r.RawText,
		Metadata:   nonNilMap(r.Metadata),
	})
	if err != nil {
		// All fields are JSON-safe; a marshal error is a programming bug.
		panic(fmt.Sprintf("hash memory content: %v", err))
	}
	return hash(raw)
}

// RequestHash identifies the exact request for Idempotency-Key replay checks.
// Unlike ContentHash it keeps external_id, so two different memories with the
// same content cannot share a default idempotency key.
func (r Request) RequestHash() string {
	raw, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("hash memory request: %v", err))
	}
	return hash(raw)
}

func nonNilList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	return values
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Masked returns a copy of the request with secrets removed from every
// user-authored field, including nested metadata strings. Persistence and LLM
// prompts must use the masked request, never the original body.
func (r Request) Masked() Request {
	masked := r
	masked.Title = mask.Text(r.Title)
	masked.Summary = mask.Text(r.Summary)
	masked.RawText = mask.Text(r.RawText)
	masked.Questions = maskList(r.Questions)
	masked.Keywords = maskList(r.Keywords)
	masked.Atoms = maskList(r.Atoms)
	masked.Metadata = maskMetadata(r.Metadata)
	return masked
}

func maskList(values []string) []string {
	if values == nil {
		return nil
	}
	masked := make([]string, len(values))
	for i, value := range values {
		masked[i] = mask.Text(value)
	}
	return masked
}

func maskMetadata(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	masked := make(map[string]any, len(values))
	for key, value := range values {
		masked[key] = maskValue(value)
	}
	return masked
}

func maskValue(value any) any {
	switch typed := value.(type) {
	case string:
		return mask.Text(typed)
	case map[string]any:
		return maskMetadata(typed)
	case []any:
		masked := make([]any, len(typed))
		for i, item := range typed {
			masked[i] = maskValue(item)
		}
		return masked
	default:
		return value
	}
}
