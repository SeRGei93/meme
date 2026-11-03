// Package auth contains the token, scope and principal primitives shared by
// the HTTP layer and the store.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"
)

// Kind is the client kind from the v1 contract.
type Kind string

const (
	KindOpenCode Kind = "opencode"
	KindJarvis   Kind = "jarvis"
	KindUI       Kind = "ui"
	KindAdmin    Kind = "admin"
)

var allKinds = []Kind{KindOpenCode, KindJarvis, KindUI, KindAdmin}

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
	Scopes     []Scope
	CreatedAt  time.Time
	LastSeenAt *time.Time
	RevokedAt  *time.Time
}

// Principal is an authenticated caller. Client is nil for the bootstrap admin.
type Principal struct {
	Client    *Client
	Scopes    []Scope
	Bootstrap bool
}

// Valid reports whether the kind is defined by the v1 contract.
func (k Kind) Valid() bool {
	return isOneOf(k, allKinds)
}

// Source maps a client kind to the source recorded on knowledge.
func (k Kind) Source() (string, bool) {
	switch k {
	case KindOpenCode:
		return "opencode", true
	case KindJarvis:
		return "jarvis", true
	default:
		return "", false
	}
}

// Valid reports whether the scope is defined by the v1 contract.
func (s Scope) Valid() bool {
	return isOneOf(s, allScopes)
}

// AllScopes returns every scope defined by the v1 contract.
func AllScopes() []Scope {
	return append([]Scope(nil), allScopes...)
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
	if !kind.Valid() {
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
