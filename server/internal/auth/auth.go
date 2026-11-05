// Package auth contains the token, scope and principal primitives shared by
// the HTTP layer and the store.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Kind is a client capability preset. A client of kind agent produces
// knowledge under its own source; ui and admin clients never do. The legacy
// values opencode and jarvis are accepted as input and normalized to agent.
type Kind string

const (
	KindAgent Kind = "agent"
	KindUI    Kind = "ui"
	KindAdmin Kind = "admin"

	// KindOpenCode and KindJarvis are legacy input aliases. Stored clients use
	// KindAgent and distinguish themselves by source.
	KindOpenCode Kind = "opencode"
	KindJarvis   Kind = "jarvis"
)

const (
	// MaxSourceLen bounds a source label. It is a slug, not free text.
	MaxSourceLen = 64
	// MaxSourceQueryLen bounds a source filter on read endpoints. Filters may
	// also use legacy labels longer than a slug.
	MaxSourceQueryLen = 100

	// DefaultSource is used when a name cannot be turned into a slug.
	DefaultSource = "agent"
)

var (
	storedKinds = []Kind{KindAgent, KindUI, KindAdmin}
	inputKinds  = []Kind{KindAgent, KindOpenCode, KindJarvis, KindUI, KindAdmin}

	sourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// Scope is a token permission from the v1 contract.
type Scope string

const (
	ScopeIngest   Scope = "ingest"
	ScopeMCPRead  Scope = "mcp:read"
	ScopeMCPWrite Scope = "mcp:write"
	ScopeUI       Scope = "ui"
	ScopeAdmin    Scope = "admin"
)

var allScopes = []Scope{ScopeIngest, ScopeMCPRead, ScopeMCPWrite, ScopeUI, ScopeAdmin}

// Client is a stored API client. Token hashes never leave the store.
type Client struct {
	ID         int64
	Name       string
	Kind       Kind
	Source     string
	Scopes     []Scope
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// KnowledgeSource returns the source recorded for this client. The token
// resolves to the client, so callers never take the source from a request body.
// ui and admin clients have no source.
func (c Client) KnowledgeSource() (string, bool) {
	if c.Source == "" {
		return "", false
	}
	return c.Source, true
}

// Principal is an authenticated caller. Client is nil for the bootstrap admin.
type Principal struct {
	Client    *Client
	Scopes    []Scope
	Bootstrap bool
}

// NormalizeKind maps the legacy input kinds to their capability preset.
func NormalizeKind(kind Kind) Kind {
	switch kind {
	case KindOpenCode, KindJarvis:
		return KindAgent
	default:
		return kind
	}
}

// Valid reports whether the kind is accepted as client input, including the
// legacy opencode and jarvis aliases.
func (k Kind) Valid() bool {
	return isOneOf(k, inputKinds)
}

// Stored reports whether the kind is a normalized value stored in the database.
func (k Kind) Stored() bool {
	return isOneOf(k, storedKinds)
}

// AllowsSource reports whether a client of this kind produces knowledge.
func (k Kind) AllowsSource() bool {
	return NormalizeKind(k) == KindAgent
}

// Valid reports whether the scope is defined by the v1 contract.
func (s Scope) Valid() bool {
	return isOneOf(s, allScopes)
}

// AllScopes returns every scope defined by the v1 contract.
func AllScopes() []Scope {
	return append([]Scope(nil), allScopes...)
}

// ValidSourceSlug reports whether a source is a valid label for a new client. It
// intentionally does not restrict which sources can be queried: existing
// records keep whatever label their client was issued with.
func ValidSourceSlug(source string) bool {
	return len(source) <= MaxSourceLen && sourcePattern.MatchString(source)
}

// DeriveSource turns a client name into a default source slug.
func DeriveSource(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case builder.Len() > 0 && !lastDash:
			builder.WriteByte('-')
			lastDash = true
		}
	}

	source := strings.Trim(builder.String(), "-")
	if len(source) > MaxSourceLen {
		source = strings.TrimRight(source[:MaxSourceLen], "-")
	}
	if source == "" {
		return DefaultSource
	}
	return source
}

// HasAnyScope reports whether the principal holds at least one of the
// required scopes. The contract defines endpoint scopes as an any-of list.
func (p Principal) HasAnyScope(required []Scope) bool {
	for _, want := range required {
		for _, have := range p.Scopes {
			if have == want {
				return true
			}
		}
	}
	return false
}

// GenerateToken creates a new opaque bearer token and its stored SHA-256 hash.
// The plaintext token is returned exactly once.
func GenerateToken(kind Kind) (token string, hash string, err error) {
	kind = NormalizeKind(kind)
	if !kind.Stored() {
		return "", "", fmt.Errorf("invalid client kind %q", kind)
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}

	token = "sb_" + string(kind) + "_" + base64.RawURLEncoding.EncodeToString(random)
	return token, HashToken(token), nil
}

// HashToken returns the hex SHA-256 hash stored in the clients table.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
