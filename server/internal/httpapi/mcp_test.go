package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func mcpDo(t *testing.T, env *testEnv, token string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	return env.doRaw(t, http.MethodPost, "/mcp", token, "application/json",
		bytes.NewReader(mustJSON(t, payload)))
}

func mcpBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode MCP body: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func mcpResult(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	body := mcpBody(t, rec)
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result: %q", rec.Body.String())
	}
	return result
}

func mcpStructured(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	structured, ok := mcpResult(t, rec)["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("result has no structuredContent: %q", rec.Body.String())
	}
	return structured
}

func toolCallPayload(id int, name string, arguments any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": arguments},
	}
}

func TestMCPInitialize(t *testing.T) {
	env := newTestEnv(t)
	token := createClientToken(t, env, "jarvis", "jarvis", []string{"mcp:read"})

	rec := mcpDo(t, env, token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	result := mcpResult(t, rec)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", result["protocolVersion"])
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("X-Request-Id header is empty")
	}
}

func TestMCPAuthAndScopes(t *testing.T) {
	env := newTestEnv(t)
	uiToken := createClientToken(t, env, "app", "ui", []string{"ui", "admin"})
	reader := createClientToken(t, env, "reader", "jarvis", []string{"mcp:read"})

	if rec := mcpDo(t, env, "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token status = %d, want 401", rec.Code)
	}
	if rec := mcpDo(t, env, uiToken, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); rec.Code != http.StatusForbidden {
		t.Errorf("ui token status = %d, want 403", rec.Code)
	}
	if rec := mcpDo(t, env, testAdminToken, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}); rec.Code != http.StatusForbidden {
		t.Errorf("bootstrap token status = %d, want 403", rec.Code)
	}

	revoked := createClient(t, env, "revoked", "jarvis", []string{"mcp:read"})
	env.do(t, http.MethodDelete, "/api/v1/clients/"+strconv.FormatInt(revoked.Client.ID, 10), testAdminToken, nil)
	if rec := mcpDo(t, env, revoked.Token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked token status = %d, want 401", rec.Code)
	}

	rec := mcpDo(t, env, reader, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reader status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	tools := mcpResult(t, rec)["tools"].([]any)
	if len(tools) != 5 {
		t.Errorf("tools = %d, want 5", len(tools))
	}
}

func TestMCPSearchReturnsCardsWithoutRawText(t *testing.T) {
	env := newTestEnv(t)
	client := createClient(t, env, "jarvis", "jarvis", []string{"mcp:read"})
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	seedReadMemory(t, env, client.Client.ID, "m1", "github.com/acme/meme", []string{"fts5", "sqlite"}, now)

	rec := mcpDo(t, env, client.Token, toolCallPayload(2, "search", map[string]any{"query": "fts5"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	structured := mcpStructured(t, rec)
	if structured["total"].(float64) != 1 {
		t.Errorf("total = %v, want 1", structured["total"])
	}
	if structured["limit"].(float64) != 8 {
		t.Errorf("limit = %v, want 8", structured["limit"])
	}
	if structured["query"] != "fts5" {
		t.Errorf("query = %v, want fts5", structured["query"])
	}
	items := structured["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if _, ok := item["raw_text"]; ok {
		t.Error("search item leaks raw_text")
	}
	if item["title"] != "m1" {
		t.Errorf("title = %v, want m1", item["title"])
	}
}

func TestMCPGetRecordReturnsRawTextAndNotFound(t *testing.T) {
	env := newTestEnv(t)
	client := createClient(t, env, "jarvis", "jarvis", []string{"mcp:read"})
	recordID := seedReadMemory(t, env, client.Client.ID, "m2", "proj", nil, time.Now().UTC())

	rec := mcpDo(t, env, client.Token, toolCallPayload(3, "get_record", map[string]any{"id": recordID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	structured := mcpStructured(t, rec)
	if structured["raw_text"] != "Raw text for m2" {
		t.Errorf("raw_text = %v", structured["raw_text"])
	}

	missing := mcpDo(t, env, client.Token, toolCallPayload(4, "get_record", map[string]any{"id": 999}))
	result := mcpResult(t, missing)
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
	errorBody := result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if errorBody["code"] != "not_found" {
		t.Errorf("code = %v, want not_found", errorBody["code"])
	}
}

func TestMCPProjectsAndStatsMatchREST(t *testing.T) {
	env := newTestEnv(t)
	client := createClient(t, env, "jarvis", "jarvis", []string{"mcp:read"})
	seedReadMemory(t, env, client.Client.ID, "m1", "proj", nil, time.Now().UTC())

	projectsRec := mcpDo(t, env, client.Token, toolCallPayload(5, "list_projects", map[string]any{}))
	restProjects := env.do(t, http.MethodGet, "/api/v1/projects", client.Token, nil)
	assertSameJSON(t, mcpStructured(t, projectsRec), restProjects.Body.Bytes(), "projects")

	statsRec := mcpDo(t, env, client.Token, toolCallPayload(6, "stats", map[string]any{}))
	restStats := env.do(t, http.MethodGet, "/api/v1/stats", client.Token, nil)
	assertSameJSON(t, mcpStructured(t, statsRec), restStats.Body.Bytes(), "stats")
}

func assertSameJSON(t *testing.T, mcpValue map[string]any, restJSON []byte, label string) {
	t.Helper()
	var rest any
	if err := json.Unmarshal(restJSON, &rest); err != nil {
		t.Fatalf("decode REST %s: %v", label, err)
	}
	if !reflect.DeepEqual(any(mcpValue), rest) {
		t.Errorf("%s MCP = %v, REST = %v", label, mcpValue, rest)
	}
}

func TestMCPRememberRequiresWriteScopeAndDerivesSource(t *testing.T) {
	env := newTestEnv(t)
	writer := createClient(t, env, "jarvis", "jarvis", []string{"mcp:write"})
	reader := createClient(t, env, "reader", "jarvis", []string{"mcp:read"})

	if rec := mcpDo(t, env, reader.Token, toolCallPayload(7, "remember", map[string]any{"text": "no write"})); rec.Code != http.StatusForbidden {
		t.Errorf("read-only remember status = %d, want 403", rec.Code)
	}

	writeOnly := createClient(t, env, "writer", "jarvis", []string{"mcp:write"})
	if rec := mcpDo(t, env, writeOnly.Token, toolCallPayload(7, "search", map[string]any{"query": "x"})); rec.Code != http.StatusForbidden {
		t.Errorf("write-only search status = %d, want 403", rec.Code)
	}

	args := map[string]any{
		"text":        "Решение: используем SQLite FTS5",
		"title":       "FTS5",
		"summary":     "Выбрали FTS5",
		"project":     "second-brain",
		"external_id": "decision-fts5",
	}
	rec := mcpDo(t, env, writer.Token, toolCallPayload(8, "remember", args))
	if rec.Code != http.StatusOK {
		t.Fatalf("remember status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	structured := mcpStructured(t, rec)
	if structured["status"] != "indexed" {
		t.Errorf("status = %v, want indexed", structured["status"])
	}
	if structured["job_id"] != nil {
		t.Errorf("job_id = %v, want null for a full card", structured["job_id"])
	}
	if structured["replayed"] != false {
		t.Errorf("replayed = %v, want false", structured["replayed"])
	}
	recordID := int64(structured["record_id"].(float64))

	replay := mcpDo(t, env, writer.Token, toolCallPayload(9, "remember", args))
	if mcpStructured(t, replay)["replayed"] != true {
		t.Error("second identical remember is not replayed")
	}

	record := env.do(t, http.MethodGet, "/api/v1/records/"+strconv.FormatInt(recordID, 10), reader.Token, nil)
	if record.Code != http.StatusOK {
		t.Fatalf("get record status = %d (body %s)", record.Code, record.Body.String())
	}
	var recordBody map[string]any
	if err := json.Unmarshal(record.Body.Bytes(), &recordBody); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if recordBody["source"] != "jarvis" {
		t.Errorf("source = %v, want jarvis", recordBody["source"])
	}

	// A source argument is rejected: source is derived from the token.
	badArgs := map[string]any{"text": "x", "source": "opencode"}
	bad := mcpDo(t, env, writer.Token, toolCallPayload(10, "remember", badArgs))
	badResult := mcpResult(t, bad)
	if badResult["isError"] != true {
		t.Error("remember with source did not fail")
	}
	badError := badResult["structuredContent"].(map[string]any)["error"].(map[string]any)
	if badError["code"] != "validation_failed" {
		t.Errorf("source error code = %v, want validation_failed", badError["code"])
	}

	// The same external_id with different content is a conflict.
	conflictArgs := map[string]any{"text": "different", "external_id": "decision-fts5"}
	conflict := mcpDo(t, env, writer.Token, toolCallPayload(11, "remember", conflictArgs))
	conflictError := mcpResult(t, conflict)["structuredContent"].(map[string]any)["error"].(map[string]any)
	if conflictError["code"] != "conflict" {
		t.Errorf("conflict code = %v, want conflict", conflictError["code"])
	}

	// The bootstrap admin has every scope but no knowledge source.
	if rec := mcpDo(t, env, testAdminToken, toolCallPayload(12, "remember", map[string]any{"text": "x"})); rec.Code != http.StatusForbidden {
		t.Errorf("bootstrap remember status = %d, want 403", rec.Code)
	}
}

func TestMCPRememberWithoutCardQueuesGeneration(t *testing.T) {
	env := newTestEnv(t)
	writer := createClient(t, env, "opencode", "opencode", []string{"mcp:write"})

	rec := mcpDo(t, env, writer.Token, toolCallPayload(12, "remember", map[string]any{
		"text": "Только текст без готовой карточки",
	}))
	structured := mcpStructured(t, rec)
	if structured["status"] != "queued" {
		t.Errorf("status = %v, want queued", structured["status"])
	}
	if structured["job_id"] == nil {
		t.Error("job_id = null, want a queued job")
	}
}

func TestMCPProtocolAndArgumentErrors(t *testing.T) {
	env := newTestEnv(t)
	token := createClientToken(t, env, "jarvis", "jarvis", []string{"mcp:read", "mcp:write"})

	malformed := env.doRaw(t, http.MethodPost, "/mcp", token, "application/json", bytes.NewReader([]byte(`{`)))
	if malformed.Code != http.StatusOK {
		t.Fatalf("malformed status = %d, want 200", malformed.Code)
	}
	if code := int(mcpBody(t, malformed)["error"].(map[string]any)["code"].(float64)); code != -32700 {
		t.Errorf("malformed code = %d, want -32700", code)
	}

	batch := env.doRaw(t, http.MethodPost, "/mcp", token, "application/json", bytes.NewReader([]byte(`[]`)))
	if code := int(mcpBody(t, batch)["error"].(map[string]any)["code"].(float64)); code != -32600 {
		t.Errorf("batch code = %d, want -32600", code)
	}

	unknownMethod := mcpDo(t, env, token, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "resources/list"})
	if code := int(mcpBody(t, unknownMethod)["error"].(map[string]any)["code"].(float64)); code != -32601 {
		t.Errorf("unknown method code = %d, want -32601", code)
	}

	unknownTool := mcpDo(t, env, token, toolCallPayload(2, "nope", map[string]any{}))
	if code := int(mcpBody(t, unknownTool)["error"].(map[string]any)["code"].(float64)); code != -32602 {
		t.Errorf("unknown tool code = %d, want -32602", code)
	}

	for _, args := range []map[string]any{
		{},
		{"query": "x", "unknown": true},
		{"query": "x", "limit": 51},
		{"query": "x", "limit": 0},
		{"query": "x", "project": ""},
		{"query": "x", "source": "other"},
	} {
		rec := mcpDo(t, env, token, toolCallPayload(3, "search", args))
		result := mcpResult(t, rec)
		if result["isError"] != true {
			t.Errorf("search args %v did not fail: %q", args, rec.Body.String())
		}
	}

	tooLarge := make([]byte, (10<<20)+1)
	oversized := env.doRaw(t, http.MethodPost, "/mcp", token, "application/json", bytes.NewReader(tooLarge))
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body status = %d, want 413", oversized.Code)
	}

	notification := mcpDo(t, env, token, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if notification.Code != http.StatusAccepted || notification.Body.Len() != 0 {
		t.Errorf("notification = %d with %d bytes, want 202 empty", notification.Code, notification.Body.Len())
	}

	// GET is only for server-initiated messages in v1.
	get := env.doRaw(t, http.MethodGet, "/mcp", token, "", nil)
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp status = %d, want 405", get.Code)
	}

	wrongType := env.doRaw(t, http.MethodPost, "/mcp", token, "text/plain", bytes.NewReader([]byte(`{}`)))
	if wrongType.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain status = %d, want 415", wrongType.Code)
	}
}
