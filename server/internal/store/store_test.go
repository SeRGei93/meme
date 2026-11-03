package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/store"
)

func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brain.db")
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close() error = %v", err)
		}
	})
	return st, path
}

func TestOpenCreatesDatabaseAndAppliesMigrations(t *testing.T) {
	st, path := openTestStore(t)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file was not created: %v", err)
	}

	version, err := st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 2 {
		t.Errorf("SchemaVersion() = %d, want 2", version)
	}

	health, err := st.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.SchemaVersion != 2 {
		t.Errorf("Health().SchemaVersion = %d, want 2", health.SchemaVersion)
	}
	if !health.FTS5 {
		t.Error("Health().FTS5 = false, want true")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brain.db")

	first, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer second.Close()

	version, err := second.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 2 {
		t.Errorf("SchemaVersion() = %d, want 2", version)
	}
}

func TestHealthFailsAfterClose(t *testing.T) {
	st, _ := openTestStore(t)
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := st.Health(context.Background()); err == nil {
		t.Fatal("Health() error = nil, want error after close")
	}
}

func TestCreateClientReturnsTokenAndLookupByHashFindsIt(t *testing.T) {
	st, _ := openTestStore(t)

	client, token, err := st.CreateClient(context.Background(), "opencode-macbook", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if client.ID <= 0 {
		t.Errorf("client.ID = %d, want > 0", client.ID)
	}
	if client.Name != "opencode-macbook" {
		t.Errorf("client.Name = %q, want %q", client.Name, "opencode-macbook")
	}
	if client.Kind != auth.KindOpenCode {
		t.Errorf("client.Kind = %q, want %q", client.Kind, auth.KindOpenCode)
	}
	if client.CreatedAt.IsZero() {
		t.Error("client.CreatedAt is zero")
	}
	if len(token) < len("sb_opencode_") {
		t.Fatalf("token = %q, want a generated token", token)
	}

	found, err := st.FindClientByTokenHash(context.Background(), auth.HashToken(token))
	if err != nil {
		t.Fatalf("FindClientByTokenHash() error = %v", err)
	}
	if found.ID != client.ID {
		t.Errorf("found.ID = %d, want %d", found.ID, client.ID)
	}
	if len(found.Scopes) != 1 || found.Scopes[0] != auth.ScopeIngest {
		t.Errorf("found.Scopes = %v, want [ingest]", found.Scopes)
	}
	if source, ok := found.Source(); !ok || source != "opencode" {
		t.Errorf("found.Source() = (%q, %v), want (opencode, true)", source, ok)
	}

	if _, err := st.FindClientByTokenHash(context.Background(), auth.HashToken("sb_ui_wrong")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FindClientByTokenHash(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestCreateClientRejectsDuplicateActiveName(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()

	if _, _, err := st.CreateClient(ctx, "jarvis", auth.KindJarvis, []auth.Scope{auth.ScopeIngest}); err != nil {
		t.Fatalf("first CreateClient() error = %v", err)
	}
	if _, _, err := st.CreateClient(ctx, "jarvis", auth.KindJarvis, []auth.Scope{auth.ScopeIngest}); !errors.Is(err, store.ErrClientNameTaken) {
		t.Errorf("second CreateClient() error = %v, want ErrClientNameTaken", err)
	}
}

func TestRevokeClientIsImmediateAndIdempotent(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()

	client, token, err := st.CreateClient(ctx, "revoked-client", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if _, err := st.FindClientByTokenHash(ctx, auth.HashToken(token)); err != nil {
		t.Fatalf("FindClientByTokenHash() before revoke error = %v", err)
	}

	if err := st.RevokeClient(ctx, client.ID); err != nil {
		t.Fatalf("RevokeClient() error = %v", err)
	}
	if _, err := st.FindClientByTokenHash(ctx, auth.HashToken(token)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("FindClientByTokenHash() after revoke error = %v, want ErrNotFound", err)
	}

	if err := st.RevokeClient(ctx, client.ID); err != nil {
		t.Errorf("second RevokeClient() error = %v, want nil (idempotent)", err)
	}
	if err := st.RevokeClient(ctx, client.ID+1000); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("RevokeClient(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestListClientsReturnsActiveAndRevokedWithoutTokenHashes(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()

	active, _, err := st.CreateClient(ctx, "active", auth.KindJarvis, []auth.Scope{auth.ScopeMCPRead})
	if err != nil {
		t.Fatalf("CreateClient(active) error = %v", err)
	}
	revoked, _, err := st.CreateClient(ctx, "revoked", auth.KindUI, []auth.Scope{auth.ScopeUI})
	if err != nil {
		t.Fatalf("CreateClient(revoked) error = %v", err)
	}
	if err := st.RevokeClient(ctx, revoked.ID); err != nil {
		t.Fatalf("RevokeClient() error = %v", err)
	}

	clients, err := st.ListClients(ctx)
	if err != nil {
		t.Fatalf("ListClients() error = %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("len(clients) = %d, want 2", len(clients))
	}
	if clients[0].ID != active.ID || clients[0].RevokedAt != nil {
		t.Errorf("clients[0] = %+v, want active client %d", clients[0], active.ID)
	}
	if clients[1].ID != revoked.ID || clients[1].RevokedAt == nil {
		t.Errorf("clients[1] = %+v, want revoked client %d with revoked_at", clients[1], revoked.ID)
	}
}

func TestCreateClientRejectsInvalidInput(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		client string
		kind   auth.Kind
		scopes []auth.Scope
	}{
		{"empty name", "", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest}},
		{"invalid kind", "client", auth.Kind("robot"), []auth.Scope{auth.ScopeIngest}},
		{"no scopes", "client", auth.KindOpenCode, nil},
		{"invalid scope", "client", auth.KindOpenCode, []auth.Scope{auth.Scope("root")}},
		{"duplicate scope", "client", auth.KindOpenCode, []auth.Scope{auth.ScopeIngest, auth.ScopeIngest}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := st.CreateClient(ctx, tt.client, tt.kind, tt.scopes); err == nil {
				t.Fatal("CreateClient() error = nil, want error")
			}
		})
	}
}
