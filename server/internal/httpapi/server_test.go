package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	if body.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", body.SchemaVersion)
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

type createClientBody struct {
	Client struct {
		ID         int64      `json:"id"`
		Name       string     `json:"name"`
		Kind       string     `json:"kind"`
		Scopes     []string   `json:"scopes"`
		Source     *string    `json:"source"`
		CreatedAt  time.Time  `json:"created_at"`
		LastSeenAt *time.Time `json:"last_seen_at"`
		RevokedAt  *time.Time `json:"revoked_at"`
	} `json:"client"`
	Token string `json:"token"`
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
