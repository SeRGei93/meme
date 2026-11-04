// Package ingest owns the spool bundle contract: decoding, validation,
// content hashing, raw-text assembly and the heuristic card written on ingest.
package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SeRGei93/meme/server/internal/mask"
)

// Role is an allowed message author.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

var allRoles = []Role{RoleUser, RoleAssistant, RoleSystem, RoleTool}

// Valid reports whether the role is part of the v1 spool contract.
func (r Role) Valid() bool {
	for _, allowed := range allRoles {
		if r == allowed {
			return true
		}
	}
	return false
}

// ToolCall is a normalized tool invocation captured by the plugin.
type ToolCall struct {
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Message is one normalized session message.
type Message struct {
	ID        string     `json:"id"`
	Role      Role       `json:"role"`
	Text      string     `json:"text"`
	At        time.Time  `json:"at"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	present messagePresence
}

type messagePresence struct {
	id, role, text, at bool
}

// Metadata is the informational capture context. Unknown keys are rejected so
// the contract stays explicit.
type Metadata map[string]string

// Bundle is one normalized OpenCode session as accepted by POST /api/v1/ingest.
// `schema_version` and `source` are intentionally absent: the server owns them.
type Bundle struct {
	BundleID    string    `json:"bundle_id"`
	ExternalID  string    `json:"external_id"`
	ProjectKey  string    `json:"project_key"`
	ProjectPath string    `json:"project_path"`
	Title       string    `json:"title"`
	Final       bool      `json:"final"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Messages    []Message `json:"messages"`
	Metadata    Metadata  `json:"metadata"`

	finalSet     bool
	startedAtSet bool
	updatedAtSet bool
	masked       bool
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

// DecodeBundle reads one JSON bundle with unknown fields rejected.
func DecodeBundle(r io.Reader) (Bundle, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Bundle{}, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Bundle{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, err
	}

	bundle.finalSet = hasField(fields, "final")
	bundle.startedAtSet = hasField(fields, "started_at")
	bundle.updatedAtSet = hasField(fields, "updated_at")
	return bundle, nil
}

func hasField(fields map[string]json.RawMessage, name string) bool {
	value, ok := fields[name]
	return ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// UnmarshalJSON tracks field presence so required-but-empty fields (notably
// `text`) can still be validated.
func (m *Message) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	type plain Message
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}

	*m = Message(decoded)
	m.present = messagePresence{
		id:   hasField(fields, "id"),
		role: hasField(fields, "role"),
		text: hasField(fields, "text"),
		at:   hasField(fields, "at"),
	}
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var metadataLimits = map[string]int{
	"agent":            100,
	"model":            200,
	"branch":           200,
	"opencode_version": 100,
}

// Validate applies the v1 spool schema plus cross-field business rules.
func (b Bundle) Validate() error {
	if b.BundleID == "" {
		return fieldError("bundle_id", "is required")
	}
	if !uuidPattern.MatchString(b.BundleID) {
		return fieldError("bundle_id", "must be a UUID")
	}
	if err := validateString("external_id", b.ExternalID, 1, 200); err != nil {
		return err
	}
	if strings.IndexFunc(b.ExternalID, unicode.IsSpace) >= 0 {
		return fieldError("external_id", "must not contain whitespace")
	}
	if err := validateString("project_key", b.ProjectKey, 1, 500); err != nil {
		return err
	}
	if err := validateString("project_path", b.ProjectPath, 1, 1000); err != nil {
		return err
	}
	if err := validateString("title", b.Title, 1, 500); err != nil {
		return err
	}
	if !b.finalSet {
		return fieldError("final", "is required")
	}
	if !b.startedAtSet {
		return fieldError("started_at", "is required")
	}
	if !b.updatedAtSet {
		return fieldError("updated_at", "is required")
	}
	if b.StartedAt.After(b.UpdatedAt) {
		return fieldError("started_at", "must be earlier than or equal to updated_at")
	}
	if b.Messages == nil {
		return fieldError("messages", "is required")
	}
	if len(b.Messages) == 0 {
		return fieldError("messages", "must contain at least one message")
	}
	if len(b.Messages) > 10000 {
		return fieldError("messages", "must contain at most 10000 messages")
	}
	for i, message := range b.Messages {
		if err := message.validate(i); err != nil {
			return err
		}
	}
	if b.Metadata == nil {
		return fieldError("metadata", "is required")
	}
	return b.Metadata.validate()
}

func (m Message) validate(index int) error {
	prefix := fmt.Sprintf("messages[%d]", index)
	if !m.present.id || m.ID == "" {
		return fieldError(prefix+".id", "is required")
	}
	if err := validateString(prefix+".id", m.ID, 1, 200); err != nil {
		return err
	}
	if !m.present.role || m.Role == "" {
		return fieldError(prefix+".role", "is required")
	}
	if !m.Role.Valid() {
		return fieldError(prefix+".role", "must be one of user, assistant, system, tool")
	}
	if !m.present.text {
		return fieldError(prefix+".text", "is required")
	}
	if err := validateMaxRunes(prefix+".text", m.Text, 200000); err != nil {
		return err
	}
	if !m.present.at {
		return fieldError(prefix+".at", "is required")
	}
	if len(m.ToolCalls) > 500 {
		return fieldError(prefix+".tool_calls", "must contain at most 500 calls")
	}
	for i, call := range m.ToolCalls {
		callPrefix := fmt.Sprintf("%s.tool_calls[%d]", prefix, i)
		if call.Name == "" {
			return fieldError(callPrefix+".name", "is required")
		}
		if err := validateString(callPrefix+".name", call.Name, 1, 200); err != nil {
			return err
		}
		if err := validateMaxRunes(callPrefix+".command", call.Command, 2000); err != nil {
			return err
		}
		if err := validateMaxRunes(callPrefix+".path", call.Path, 2000); err != nil {
			return err
		}
	}
	return nil
}

func (m Metadata) validate() error {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	// Validate in a stable order so a bad bundle always reports the same field.
	sort.Strings(keys)
	for _, key := range keys {
		limit, ok := metadataLimits[key]
		if !ok {
			return fieldError("metadata."+key, "is not an allowed field")
		}
		if err := validateMaxRunes("metadata."+key, m[key], limit); err != nil {
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

// Masked returns a copy of the bundle with secrets removed from every
// user-authored content field. Persistence and LLM prompt builders must use the
// masked bundle, never the original request. Structural identifiers and
// project coordinates stay intact because they are lookup keys, not content.
func (b Bundle) Masked() Bundle {
	masked := b
	masked.masked = true
	masked.Title = mask.Text(b.Title)

	if b.Messages != nil {
		masked.Messages = make([]Message, len(b.Messages))
		for i, message := range b.Messages {
			message.Text = mask.Text(message.Text)
			if message.ToolCalls != nil {
				calls := make([]ToolCall, len(message.ToolCalls))
				for j, call := range message.ToolCalls {
					call.Command = mask.Text(call.Command)
					call.Path = mask.Text(call.Path)
					calls[j] = call
				}
				message.ToolCalls = calls
			}
			masked.Messages[i] = message
		}
	}

	if b.Metadata != nil {
		masked.Metadata = make(Metadata, len(b.Metadata))
		for key, value := range b.Metadata {
			masked.Metadata[key] = mask.Text(value)
		}
	}

	return masked
}

// ContentHash identifies the knowledge content of a session. `updated_at` is
// excluded deliberately: a live capture that only reports activity must not
// create a new revision.
func (b Bundle) ContentHash() string {
	type content struct {
		ExternalID  string    `json:"external_id"`
		ProjectKey  string    `json:"project_key"`
		ProjectPath string    `json:"project_path"`
		Title       string    `json:"title"`
		Final       bool      `json:"final"`
		StartedAt   time.Time `json:"started_at"`
		Messages    []Message `json:"messages"`
		Metadata    Metadata  `json:"metadata"`
	}
	raw, err := json.Marshal(content{
		ExternalID:  b.ExternalID,
		ProjectKey:  b.ProjectKey,
		ProjectPath: b.ProjectPath,
		Title:       b.Title,
		Final:       b.Final,
		StartedAt:   b.StartedAt,
		Messages:    b.Messages,
		Metadata:    b.Metadata,
	})
	if err != nil {
		// All fields are JSON-safe; a marshal error is a programming bug.
		panic(fmt.Sprintf("hash ingest bundle: %v", err))
	}
	return hash(raw)
}

// RequestHash identifies the exact request body for Idempotency-Key replay
// checks.
func (b Bundle) RequestHash() string {
	raw, err := json.Marshal(b)
	if err != nil {
		panic(fmt.Sprintf("hash ingest request: %v", err))
	}
	return hash(raw)
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// RawText renders messages in the human-readable form stored on the record.
// Secrets are masked here as a last line of defence so prompt builders and
// callers that skip Bundle.Masked still cannot leak credentials.
func (b Bundle) RawText() string {
	parts := make([]string, 0, len(b.Messages))
	for _, message := range b.Messages {
		line := fmt.Sprintf("%s: %s", message.Role, message.Text)
		tools := make([]string, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			target := call.Command
			if call.Path != "" {
				if target != "" {
					target += " → "
				}
				target += call.Path
			}
			if target != "" {
				tools = append(tools, fmt.Sprintf("[tool %s: %s]", call.Name, target))
			} else {
				tools = append(tools, fmt.Sprintf("[tool %s]", call.Name))
			}
		}
		if len(tools) > 0 {
			line += "\n" + strings.Join(tools, "\n")
		}
		parts = append(parts, line)
	}
	text := strings.Join(parts, "\n\n")
	if b.masked {
		return text
	}
	return mask.Text(text)
}

// Card is the card-shaped part of a record.
type Card struct {
	Title     string
	Summary   string
	Questions []string
	Keywords  []string
	Atoms     []string
}

// HeuristicCard builds the cheap fallback card written before (or instead of)
// LLM generation: the bundle title plus the first useful assistant reply.
// Content is masked so a future prompt built from the card is safe even when a
// caller passes the original bundle.
func (b Bundle) HeuristicCard() Card {
	summary := firstText(b.Messages, RoleAssistant)
	if summary == "" {
		summary = firstText(b.Messages, RoleUser)
	}
	title := b.Title
	if !b.masked {
		title = mask.Text(title)
		summary = mask.Text(summary)
	}
	return Card{
		Title:     title,
		Summary:   truncateRunes(summary, 1000),
		Questions: []string{},
		Keywords:  []string{},
		Atoms:     []string{},
	}
}

func firstText(messages []Message, role Role) string {
	for _, message := range messages {
		if message.Role == role && strings.TrimSpace(message.Text) != "" {
			return strings.TrimSpace(message.Text)
		}
	}
	return ""
}

func truncateRunes(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "…"
}
