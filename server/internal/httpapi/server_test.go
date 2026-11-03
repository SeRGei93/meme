package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SeRGei93/meme/server/internal/httpapi"
	"github.com/SeRGei93/meme/server/internal/store"
)

const testAdminToken = "bootstrap-admin-token-for-tests"

type testEnv struct {
	handler http.Handler
	store   *store.Store
	logs    *bytes.Buffer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "brain.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := httpapi.New(httpapi.Options{
		Store:      st,
		AdminToken: testAdminToken,
		Version:    "test",
		Logger:     logger,
	})
	t.Cleanup(func() {
		_ = st.Close()
	})
	return &testEnv{handler: handler, store: st, logs: &logs}
}

func (e *testEnv) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	return e.doRaw(t, method, path, token, "application/json", reader)
}

func (e *testEnv) doRaw(t *testing.T, method, path, token, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

type errorEnvelope struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"request_id"`
	} `json:"error"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v (body %q)", err, rec.Body.String())
	}
	return envelope
}

type healthBody struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schema_version"`
	Time          time.Time `json:"time"`
	Database      struct {
		Status string `json:"status"`
		FTS5   bool   `json:"fts5"`
		Error  string `json:"error"`
	} `json:"database"`
}

func TestHealthReturnsDatabaseState(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodGet, "/health", "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header is empty")
	}

	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if body.Version != "test" {
		t.Errorf("version = %q, want test", body.Version)
	}
	if body.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", body.SchemaVersion)
	}
	if body.Time.IsZero() {
		t.Error("time is zero")
	}
	if body.Database.Status != "ok" {
		t.Errorf("database.status = %q, want ok", body.Database.Status)
	}
	if !body.Database.FTS5 {
		t.Error("database.fts5 = false, want true")
	}
}

func TestHealthReturnsDegradedWhenDatabaseIsClosed(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	rec := env.do(t, http.MethodGet, "/health", "", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	var body healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("status = %q, want degraded", body.Status)
	}
	if body.Database.Status != "error" {
		t.Errorf("database.status = %q, want error", body.Database.Status)
	}
	if body.Database.Error == "" {
		t.Error("database.error is empty")
	}
}

func TestHealthRejectsWrongMethod(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodPost, "/health", "", nil)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET" {
		t.Errorf("Allow = %q, want GET", got)
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "method_not_allowed" {
		t.Errorf("code = %q, want method_not_allowed", envelope.Error.Code)
	}
	if envelope.Error.RequestID == "" || envelope.Error.RequestID != rec.Header().Get("X-Request-Id") {
		t.Errorf("request_id = %q, header = %q", envelope.Error.RequestID, rec.Header().Get("X-Request-Id"))
	}
}

func TestUnknownRouteReturnsJSONNotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodGet, "/api/v1/does-not-exist", "", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", envelope.Error.Code)
	}
}

func TestAuthRequiresBearerToken(t *testing.T) {
	env := newTestEnv(t)
	body := map[string]any{"name": "client", "kind": "ui", "scopes": []string{"ui"}}

	tests := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"unknown token", "Bearer sb_ui_unknown"},
		{"wrong scheme", "Basic dXNlcjpwYXNz"},
		{"empty bearer", "Bearer "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/clients", bytes.NewReader(mustJSON(t, body)))
			req.Header.Set("Content-Type", "application/json")
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			env.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != "unauthorized" {
				t.Errorf("code = %q, want unauthorized", envelope.Error.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want Bearer challenge", got)
			}
		})
	}
}

type clientBody struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Scopes     []string   `json:"scopes"`
	Source     *string    `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type createClientBody struct {
	Client clientBody `json:"client"`
	Token  string     `json:"token"`
}

type clientListBody struct {
	Items []clientBody `json:"items"`
}

func TestListClientsReturnsClientsWithoutTokens(t *testing.T) {
	env := newTestEnv(t)
	created := createClient(t, env, "opencode-macbook", "opencode", []string{"ingest"})

	rec := env.do(t, http.MethodGet, "/api/v1/clients", testAdminToken, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body clientListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(body.Items))
	}
	got := body.Items[0]
	if got.ID != created.Client.ID || got.Name != created.Client.Name || got.Kind != created.Client.Kind {
		t.Errorf("items[0] = %+v, want created client %+v", got, created.Client)
	}
	if got.Source == nil || *got.Source != "opencode" {
		t.Errorf("items[0].source = %v, want opencode", got.Source)
	}
	if strings.Contains(rec.Body.String(), created.Token) ||
		strings.Contains(rec.Body.String(), "token_hash") ||
		strings.Contains(rec.Body.String(), `"token"`) {
		t.Error("list response exposes token material")
	}
}

func TestRevokeClientIsImmediateAndIdempotent(t *testing.T) {
	env := newTestEnv(t)
	created := createClient(t, env, "revocable", "ui", []string{"admin"})
	clientPath := fmt.Sprintf("/api/v1/clients/%d", created.Client.ID)

	rec := env.do(t, http.MethodDelete, clientPath, testAdminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first delete: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("first delete: body = %q, want empty", rec.Body.String())
	}

	rec = env.do(t, http.MethodGet, "/api/v1/clients", created.Token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token: status = %d, want 401 (body %s)", rec.Code, rec.Body.String())
	}

	rec = env.do(t, http.MethodGet, "/api/v1/clients", testAdminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list clients: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var list clientListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(list.Items))
	}
	if list.Items[0].RevokedAt == nil {
		t.Error("items[0].revoked_at = nil, want a revocation timestamp")
	}

	rec = env.do(t, http.MethodDelete, clientPath, testAdminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("second delete: status = %d, want 204 (idempotent)", rec.Code)
	}

	rec = env.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/clients/%d", created.Client.ID+1000), testAdminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Error.Code; code != "not_found" {
		t.Errorf("delete unknown: code = %q, want not_found", code)
	}

	rec = env.do(t, http.MethodDelete, "/api/v1/clients/not-a-number", testAdminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete invalid id: status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "invalid_request" {
		t.Errorf("delete invalid id: code = %q, want invalid_request", envelope.Error.Code)
	}
	if field := envelope.Error.Details["field"]; field != "id" {
		t.Errorf("delete invalid id: details.field = %v, want id", field)
	}

	for _, invalidPath := range []string{clientPath + "/extra", "/api/v1/clients/"} {
		rec = env.do(t, http.MethodDelete, invalidPath, testAdminToken, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("delete %q: status = %d, want 404", invalidPath, rec.Code)
		}
	}
}

func TestRotateClientReusesNameAndInvalidatesOldToken(t *testing.T) {
	env := newTestEnv(t)
	adminToken := createClientToken(t, env, "admin-client", "admin", []string{"admin"})
	old := createClient(t, env, "opencode-main", "opencode", []string{"ingest", "admin"})

	rec := env.do(t, http.MethodDelete, fmt.Sprintf("/api/v1/clients/%d", old.Client.ID), adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke old client: status = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	fresh := createClient(t, env, "opencode-main", "opencode", []string{"ingest", "admin"})
	if fresh.Token == old.Token {
		t.Error("rotated client received the same token as the revoked client")
	}
	if fresh.Client.ID == old.Client.ID {
		t.Error("rotated client reused the revoked client id")
	}

	rec = env.do(t, http.MethodGet, "/api/v1/clients", old.Token, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("old token after rotation: status = %d, want 401", rec.Code)
	}

	rec = env.do(t, http.MethodGet, "/api/v1/clients", fresh.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("new token after rotation: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var list clientListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	items := make(map[int64]clientBody, len(list.Items))
	for _, item := range list.Items {
		items[item.ID] = item
	}
	oldItem, ok := items[old.Client.ID]
	if !ok {
		t.Fatalf("list is missing revoked client %d", old.Client.ID)
	}
	if oldItem.RevokedAt == nil {
		t.Error("old client revoked_at = nil, want a revocation timestamp")
	}
	freshItem, ok := items[fresh.Client.ID]
	if !ok {
		t.Fatalf("list is missing rotated client %d", fresh.Client.ID)
	}
	if freshItem.RevokedAt != nil {
		t.Errorf("new client revoked_at = %v, want null", freshItem.RevokedAt)
	}
}

func TestBootstrapAdminCreatesFirstClient(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, map[string]any{
		"name":   "opencode-macbook",
		"kind":   "opencode",
		"scopes": []string{"ingest"},
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var body createClientBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Client.ID <= 0 {
		t.Errorf("client.id = %d, want > 0", body.Client.ID)
	}
	if body.Client.Name != "opencode-macbook" {
		t.Errorf("client.name = %q, want opencode-macbook", body.Client.Name)
	}
	if body.Client.Kind != "opencode" {
		t.Errorf("client.kind = %q, want opencode", body.Client.Kind)
	}
	if len(body.Client.Scopes) != 1 || body.Client.Scopes[0] != "ingest" {
		t.Errorf("client.scopes = %v, want [ingest]", body.Client.Scopes)
	}
	if body.Client.Source == nil || *body.Client.Source != "opencode" {
		t.Errorf("client.source = %v, want opencode", body.Client.Source)
	}
	if body.Client.CreatedAt.IsZero() {
		t.Error("client.created_at is zero")
	}
	if !regexp.MustCompile(`^sb_opencode_[A-Za-z0-9_-]{43}$`).MatchString(body.Token) {
		t.Errorf("token = %q, want sb_opencode_<43 base64url chars>", body.Token)
	}
	if strings.Contains(rec.Body.String(), "token_hash") {
		t.Error("response exposes token_hash")
	}
}

func TestCreateClientDerivesSourceFromKindAndRejectsSourceField(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		kind       string
		wantSource string
	}{
		{"opencode", "opencode"},
		{"jarvis", "jarvis"},
		{"ui", ""},
		{"admin", ""},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			body := createClient(t, env, "source-"+tt.kind, tt.kind, []string{"ui"})
			if tt.wantSource == "" {
				if body.Client.Source != nil {
					t.Errorf("client.source = %q, want null", *body.Client.Source)
				}
				return
			}
			if body.Client.Source == nil || *body.Client.Source != tt.wantSource {
				t.Errorf("client.source = %v, want %q", body.Client.Source, tt.wantSource)
			}
		})
	}

	rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, map[string]any{
		"name": "spoofed-source", "kind": "ui", "scopes": []string{"ui"}, "source": "jarvis",
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("source field: status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "validation_failed" {
		t.Errorf("source field: code = %q, want validation_failed", envelope.Error.Code)
	}
	if field := envelope.Error.Details["field"]; field != "source" {
		t.Errorf("source field: details.field = %v, want source", field)
	}
}

func TestIssuedTokensAuthenticateAndScopesAreEnforced(t *testing.T) {
	env := newTestEnv(t)

	adminToken := createClientToken(t, env, "second-admin", "admin", []string{"admin"})
	rec := env.do(t, http.MethodPost, "/api/v1/clients", adminToken, map[string]any{
		"name":   "created-by-client-token",
		"kind":   "jarvis",
		"scopes": []string{"ingest", "mcp:read", "mcp:write"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin-scoped client token: status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}

	uiToken := createClientToken(t, env, "ui-client", "ui", []string{"ui"})
	rec = env.do(t, http.MethodPost, "/api/v1/clients", uiToken, map[string]any{
		"name":   "forbidden",
		"kind":   "ui",
		"scopes": []string{"ui"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ui-scoped client token: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "forbidden" {
		t.Errorf("code = %q, want forbidden", envelope.Error.Code)
	}
	details, _ := envelope.Error.Details["required_any_of"].([]any)
	if len(details) != 1 || details[0] != "admin" {
		t.Errorf("details.required_any_of = %v, want [admin]", envelope.Error.Details["required_any_of"])
	}
}

func TestClientEndpointsRejectTokensWithoutAdminScope(t *testing.T) {
	env := newTestEnv(t)
	uiToken := createClientToken(t, env, "ui-client", "ui", []string{"ui"})
	target := createClient(t, env, "target", "ui", []string{"ui"})

	tests := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"list", http.MethodGet, "/api/v1/clients", nil},
		{"create", http.MethodPost, "/api/v1/clients", map[string]any{
			"name": "forbidden-create", "kind": "ui", "scopes": []string{"ui"},
		}},
		{"revoke", http.MethodDelete, fmt.Sprintf("/api/v1/clients/%d", target.Client.ID), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.do(t, tt.method, tt.path, uiToken, tt.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != "forbidden" {
				t.Errorf("code = %q, want forbidden", envelope.Error.Code)
			}
			required, _ := envelope.Error.Details["required_any_of"].([]any)
			if len(required) != 1 || required[0] != "admin" {
				t.Errorf("details.required_any_of = %v, want [admin]", envelope.Error.Details["required_any_of"])
			}
		})
	}

	rec := env.do(t, http.MethodGet, "/api/v1/clients", testAdminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list clients: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var list clientListBody
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, item := range list.Items {
		if item.ID == target.Client.ID && item.RevokedAt != nil {
			t.Error("forbidden revoke request revoked the target client")
		}
	}
}

func TestCreateClientValidation(t *testing.T) {
	env := newTestEnv(t)

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name:       "missing name",
			body:       mustJSON(t, map[string]any{"kind": "ui", "scopes": []string{"ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "name",
		},
		{
			name:       "invalid name characters",
			body:       mustJSON(t, map[string]any{"name": "bad name", "kind": "ui", "scopes": []string{"ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "name",
		},
		{
			name:       "name too long",
			body:       mustJSON(t, map[string]any{"name": strings.Repeat("a", 101), "kind": "ui", "scopes": []string{"ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "name",
		},
		{
			name:       "invalid kind",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "robot", "scopes": []string{"ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "kind",
		},
		{
			name:       "missing scopes",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "ui"}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "scopes",
		},
		{
			name:       "duplicate scopes",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "ui", "scopes": []string{"ui", "ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "scopes",
		},
		{
			name:       "unknown scope",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "ui", "scopes": []string{"root"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "scopes",
		},
		{
			name:       "too many scopes",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "ui", "scopes": []string{"ui", "admin", "ingest", "mcp:read", "mcp:write", "ui"}}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "scopes",
		},
		{
			name:       "unknown field",
			body:       mustJSON(t, map[string]any{"name": "client", "kind": "ui", "scopes": []string{"ui"}, "extra": true}),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "extra",
		},
		{
			name:       "wrong field type",
			body:       []byte(`{"name":"client","kind":"ui","scopes":"ui"}`),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "validation_failed",
			wantField:  "scopes",
		},
		{
			name:       "malformed json",
			body:       []byte(`{"name":`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "empty body",
			body:       nil,
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "trailing json",
			body:       []byte(`{"name":"client","kind":"ui","scopes":["ui"]}{}`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.doRaw(t, http.MethodPost, "/api/v1/clients", testAdminToken, "application/json", bytes.NewReader(tt.body))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", envelope.Error.Code, tt.wantCode)
			}
			if tt.wantField != "" {
				if got := envelope.Error.Details["field"]; got != tt.wantField {
					t.Errorf("details.field = %v, want %q", got, tt.wantField)
				}
			}
		})
	}
}

func TestCreateClientRejectsUnsupportedMediaType(t *testing.T) {
	env := newTestEnv(t)

	rec := env.doRaw(t, http.MethodPost, "/api/v1/clients", testAdminToken, "text/plain", strings.NewReader(`{}`))

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	if code := decodeError(t, rec).Error.Code; code != "unsupported_media_type" {
		t.Errorf("code = %q, want unsupported_media_type", code)
	}
}

func TestCreateClientAcceptsJSONWithCharset(t *testing.T) {
	env := newTestEnv(t)

	rec := env.doRaw(t, http.MethodPost, "/api/v1/clients", testAdminToken, "application/json; charset=utf-8",
		strings.NewReader(`{"name":"client","kind":"ui","scopes":["ui"]}`))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestCreateClientRejectsDuplicateActiveName(t *testing.T) {
	env := newTestEnv(t)
	body := map[string]any{"name": "duplicate", "kind": "ui", "scopes": []string{"ui"}}

	if rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, body); rec.Code != http.StatusCreated {
		t.Fatalf("first create: status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create: status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Error.Code; code != "conflict" {
		t.Errorf("code = %q, want conflict", code)
	}
}

func TestCreateClientRejectsOversizedBody(t *testing.T) {
	env := newTestEnv(t)

	body := []byte(`{"name":"` + strings.Repeat("a", 10<<20) + `","kind":"ui","scopes":["ui"]}`)
	rec := env.doRaw(t, http.MethodPost, "/api/v1/clients", testAdminToken, "application/json", bytes.NewReader(body))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if code := decodeError(t, rec).Error.Code; code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", code)
	}
}

func TestRequestLogsAreStructuredAndDoNotContainSecrets(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, map[string]any{
		"name":   "logged-client",
		"kind":   "opencode",
		"scopes": []string{"ingest"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var created createClientBody
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	scanner := bufio.NewScanner(env.logs)
	foundRequestLog := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.Contains(string(line), testAdminToken) {
			t.Fatalf("log line contains the admin token: %s", line)
		}
		if strings.Contains(string(line), created.Token) {
			t.Fatalf("log line contains the issued client token: %s", line)
		}

		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("log line is not structured JSON: %v (%s)", err, line)
		}
		if entry["msg"] == "http request" {
			foundRequestLog = true
			if entry["request_id"] == "" || entry["path"] != "/api/v1/clients" || entry["status"] == nil {
				t.Errorf("request log is missing fields: %v", entry)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read logs: %v", err)
	}
	if !foundRequestLog {
		t.Error("no http request log entry found")
	}
}

func createClient(t *testing.T, env *testEnv, name, kind string, scopes []string) createClientBody {
	t.Helper()
	rec := env.do(t, http.MethodPost, "/api/v1/clients", testAdminToken, map[string]any{
		"name":   name,
		"kind":   kind,
		"scopes": scopes,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create client %q: status = %d, want 201 (body %s)", name, rec.Code, rec.Body.String())
	}
	var body createClientBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode client %q: %v", name, err)
	}
	return body
}

func createClientToken(t *testing.T, env *testEnv, name, kind string, scopes []string) string {
	t.Helper()
	return createClient(t, env, name, kind, scopes).Token
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return raw
}
