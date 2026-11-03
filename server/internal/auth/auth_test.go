package auth_test

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/auth"
)

func TestGenerateTokenUsesContractFormat(t *testing.T) {
	token, hash, err := auth.GenerateToken(auth.KindOpenCode)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	if !regexp.MustCompile(`^sb_opencode_[A-Za-z0-9_-]{43}$`).MatchString(token) {
		t.Errorf("token = %q, want sb_opencode_ followed by 43 base64url characters", token)
	}
	randomPart := token[len("sb_opencode_"):]
	raw, err := base64.RawURLEncoding.DecodeString(randomPart)
	if err != nil {
		t.Fatalf("token payload is not base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("token payload length = %d, want 32", len(raw))
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex characters", len(hash))
	}
	if hash == token {
		t.Error("hash equals plaintext token")
	}
}

func TestGenerateTokenIsUnique(t *testing.T) {
	first, _, err := auth.GenerateToken(auth.KindJarvis)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	second, _, err := auth.GenerateToken(auth.KindJarvis)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if first == second {
		t.Error("two generated tokens are equal")
	}
}

func TestHashTokenIsStable(t *testing.T) {
	const token = "sb_ui_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	first := auth.HashToken(token)
	second := auth.HashToken(token)
	if first != second {
		t.Errorf("hashes differ: %q != %q", first, second)
	}
	if first == token {
		t.Error("hash equals plaintext token")
	}
}

func TestKindSource(t *testing.T) {
	tests := []struct {
		kind   auth.Kind
		source string
		ok     bool
		valid  bool
	}{
		{auth.KindOpenCode, "opencode", true, true},
		{auth.KindJarvis, "jarvis", true, true},
		{auth.KindUI, "", false, true},
		{auth.KindAdmin, "", false, true},
		{auth.Kind("other"), "", false, false},
	}
	for _, tt := range tests {
		source, ok := tt.kind.Source()
		if source != tt.source || ok != tt.ok {
			t.Errorf("%q.Source() = (%q, %v), want (%q, %v)", tt.kind, source, ok, tt.source, tt.ok)
		}
		if got := tt.kind.Valid(); got != tt.valid {
			t.Errorf("%q.Valid() = %v, want %v", tt.kind, got, tt.valid)
		}
	}
}

func TestScopeValid(t *testing.T) {
	for _, scope := range auth.AllScopes() {
		if !scope.Valid() {
			t.Errorf("%q.Valid() = false, want true", scope)
		}
	}
	if auth.Scope("root").Valid() {
		t.Error(`Scope("root").Valid() = true, want false`)
	}
}

func TestValidateClient(t *testing.T) {
	valid := []struct {
		name   string
		client string
		kind   auth.Kind
		scopes []auth.Scope
	}{
		{"opencode", "opencode-macbook", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest}},
		{"jarvis", "jarvis", auth.KindJarvis, []auth.Scope{auth.ScopeIngest, auth.ScopeMCPRead, auth.ScopeMCPWrite}},
		{"ui", "second-brain-app", auth.KindUI, []auth.Scope{auth.ScopeUI, auth.ScopeAdmin}},
	}
	for _, tt := range valid {
		if err := auth.ValidateClient(tt.client, tt.kind, tt.scopes); err != nil {
			t.Errorf("%s: ValidateClient() error = %v, want nil", tt.name, err)
		}
	}

	invalid := []struct {
		name   string
		client string
		kind   auth.Kind
		scopes []auth.Scope
		field  string
	}{
		{"missing name", "", auth.KindUI, []auth.Scope{auth.ScopeUI}, "name"},
		{"name too long", strings.Repeat("a", 101), auth.KindUI, []auth.Scope{auth.ScopeUI}, "name"},
		{"bad name", "bad name", auth.KindUI, []auth.Scope{auth.ScopeUI}, "name"},
		{"bad kind", "client", auth.Kind("robot"), []auth.Scope{auth.ScopeUI}, "kind"},
		{"no scopes", "client", auth.KindUI, nil, "scopes"},
		{"too many scopes", "client", auth.KindUI, []auth.Scope{auth.ScopeUI, auth.ScopeAdmin, auth.ScopeIngest, auth.ScopeMCPRead, auth.ScopeMCPWrite, auth.ScopeUI}, "scopes"},
		{"unknown scope", "client", auth.KindUI, []auth.Scope{auth.Scope("root")}, "scopes"},
		{"duplicate scope", "client", auth.KindUI, []auth.Scope{auth.ScopeUI, auth.ScopeUI}, "scopes"},
	}
	for _, tt := range invalid {
		err := auth.ValidateClient(tt.client, tt.kind, tt.scopes)
		var validationError *auth.ValidationError
		if !errors.As(err, &validationError) {
			t.Errorf("%s: ValidateClient() error = %v, want *ValidationError", tt.name, err)
			continue
		}
		if validationError.Field != tt.field {
			t.Errorf("%s: field = %q, want %q", tt.name, validationError.Field, tt.field)
		}
	}
}

func TestPrincipalHasAnyScope(t *testing.T) {
	principal := auth.Principal{Scopes: []auth.Scope{auth.ScopeIngest}}
	if !principal.HasAnyScope([]auth.Scope{auth.ScopeAdmin, auth.ScopeIngest}) {
		t.Error("HasAnyScope() = false, want true for an any-of match")
	}
	if principal.HasAnyScope([]auth.Scope{auth.ScopeAdmin}) {
		t.Error("HasAnyScope() = true, want false when no scope matches")
	}
	if principal.HasAnyScope(nil) {
		t.Error("HasAnyScope(nil) = true, want false")
	}
}
