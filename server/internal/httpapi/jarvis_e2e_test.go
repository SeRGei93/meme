package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// TestJarvisMCPEndToEnd walks the primary integration path: an admin issues a
// jarvis client, Jarvis remembers and searches over MCP, the app sees the
// record, and revoking the client blocks both operations.
func TestJarvisMCPEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	jarvis := createClientWithSource(t, env, "jarvis", "agent", "jarvis",
		[]string{"ingest", "mcp:read", "mcp:write"})
	ui := createClientToken(t, env, "second-brain-app", "ui", []string{"ui", "admin"})

	init := mcpDo(t, env, jarvis.Token, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{},
	})
	if init.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body %s)", init.Code, init.Body.String())
	}
	if got := mcpResult(t, init)["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", got)
	}

	tools := mcpDo(t, env, jarvis.Token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	listed := mcpResult(t, tools)["tools"].([]any)
	if len(listed) != 5 {
		t.Errorf("tools = %d, want 5", len(listed))
	}

	remember := map[string]any{
		"text":        "Решение: Jarvis пишет память через MCP remember.",
		"title":       "Jarvis MCP",
		"summary":     "Проверка основного пути интеграции.",
		"project":     "second-brain",
		"tags":        []string{"jarvis", "mcp"},
		"external_id": "jarvis-e2e-remember-1",
	}
	first := mcpDo(t, env, jarvis.Token, toolCallPayload(3, "remember", remember))
	if first.Code != http.StatusOK {
		t.Fatalf("remember status = %d, want 200 (body %s)", first.Code, first.Body.String())
	}
	firstResult := mcpStructured(t, first)
	if firstResult["status"] != "indexed" {
		t.Errorf("status = %v, want indexed", firstResult["status"])
	}
	if firstResult["replayed"] != false {
		t.Errorf("replayed = %v, want false on first write", firstResult["replayed"])
	}
	recordID := int64(firstResult["record_id"].(float64))

	replay := mcpDo(t, env, jarvis.Token, toolCallPayload(4, "remember", remember))
	replayResult := mcpStructured(t, replay)
	if replayResult["replayed"] != true {
		t.Error("second identical remember was not replayed")
	}
	if int64(replayResult["record_id"].(float64)) != recordID {
		t.Errorf("replayed record_id = %v, want %d", replayResult["record_id"], recordID)
	}

	// Source is owned by the token and cannot be smuggled into remember.
	spoof := mcpDo(t, env, jarvis.Token, toolCallPayload(5, "remember", map[string]any{
		"text": "spoof", "source": "opencode",
	}))
	spoofResult := mcpResult(t, spoof)
	if spoofResult["isError"] != true {
		t.Fatal("remember with a source argument was accepted")
	}
	spoofError := spoofResult["structuredContent"].(map[string]any)["error"].(map[string]any)
	if spoofError["code"] != "validation_failed" {
		t.Errorf("spoof error code = %v, want validation_failed", spoofError["code"])
	}

	search := mcpDo(t, env, jarvis.Token, toolCallPayload(6, "search", map[string]any{
		"query": "Jarvis", "source": "jarvis",
	}))
	searchResult := mcpStructured(t, search)
	items := searchResult["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("search items = %d, want 1", len(items))
	}
	if items[0].(map[string]any)["source"] != "jarvis" {
		t.Errorf("search source = %v, want jarvis", items[0].(map[string]any)["source"])
	}

	// The app reads the same record with its UI token.
	record := env.do(t, http.MethodGet, "/api/v1/records/"+strconv.FormatInt(recordID, 10), ui, nil)
	if record.Code != http.StatusOK {
		t.Fatalf("record status = %d, want 200 (body %s)", record.Code, record.Body.String())
	}
	var recordBody map[string]any
	if err := json.Unmarshal(record.Body.Bytes(), &recordBody); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if recordBody["source"] != "jarvis" {
		t.Errorf("record source = %v, want jarvis", recordBody["source"])
	}
	appSearch := env.do(t, http.MethodGet, "/api/v1/search?q="+url.QueryEscape("Jarvis")+"&source=jarvis", ui, nil)
	if appSearch.Code != http.StatusOK {
		t.Fatalf("app search status = %d, want 200 (body %s)", appSearch.Code, appSearch.Body.String())
	}
	if total := decodeSearch(t, appSearch).Total; total != 1 {
		t.Errorf("app search total = %d, want 1", total)
	}

	revoke := env.do(t, http.MethodDelete,
		fmt.Sprintf("/api/v1/clients/%d", jarvis.Client.ID), testAdminToken, nil)
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204 (body %s)", revoke.Code, revoke.Body.String())
	}

	if rec := mcpDo(t, env, jarvis.Token, toolCallPayload(7, "remember", map[string]any{
		"text": "after revoke", "external_id": "jarvis-e2e-after-revoke",
	})); rec.Code != http.StatusUnauthorized {
		t.Errorf("remember after revoke status = %d, want 401", rec.Code)
	}
	if rec := mcpDo(t, env, jarvis.Token, toolCallPayload(8, "search", map[string]any{
		"query": "Jarvis",
	})); rec.Code != http.StatusUnauthorized {
		t.Errorf("search after revoke status = %d, want 401", rec.Code)
	}
}

// TestJarvisHTTPFallbackEndToEnd covers clients that cannot speak MCP: the same
// token uses POST /api/v1/memories and GET /api/v1/search.
func TestJarvisHTTPFallbackEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	jarvis := createClientWithSource(t, env, "jarvis-http", "agent", "jarvis",
		[]string{"ingest", "mcp:read"})

	memory := map[string]any{
		"external_id": "jarvis-http-1",
		"project_key": "second-brain",
		"title":       "Jarvis HTTP fallback",
		"summary":     "Проверка резервного пути.",
		"raw_text":    "Jarvis uses HTTP when MCP is unavailable.",
	}
	first := env.do(t, http.MethodPost, "/api/v1/memories", jarvis.Token, memory)
	if first.Code != http.StatusCreated {
		t.Fatalf("memory status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}
	var created memoryResponseBody
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode memory: %v", err)
	}
	if created.Replayed {
		t.Error("first memory was replayed")
	}

	replay := env.do(t, http.MethodPost, "/api/v1/memories", jarvis.Token, memory)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body %s)", replay.Code, replay.Body.String())
	}
	var replayed memoryResponseBody
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if !replayed.Replayed || replayed.RecordID != created.RecordID {
		t.Errorf("replay = %+v, want replayed record %d", replayed, created.RecordID)
	}

	spoof := map[string]any{}
	for key, value := range memory {
		spoof[key] = value
	}
	spoof["source"] = "opencode"
	if rec := env.do(t, http.MethodPost, "/api/v1/memories", jarvis.Token, spoof); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("memory with source status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}

	found := env.do(t, http.MethodGet,
		"/api/v1/search?q=Jarvis&source=jarvis", jarvis.Token, nil)
	if found.Code != http.StatusOK {
		t.Fatalf("search status = %d, want 200 (body %s)", found.Code, found.Body.String())
	}
	if total := decodeSearch(t, found).Total; total != 1 {
		t.Errorf("search total = %d, want 1", total)
	}

	if rec := env.do(t, http.MethodDelete,
		fmt.Sprintf("/api/v1/clients/%d", jarvis.Client.ID), testAdminToken, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, "/api/v1/memories", jarvis.Token, memory); rec.Code != http.StatusUnauthorized {
		t.Errorf("memory after revoke status = %d, want 401", rec.Code)
	}
	if rec := env.do(t, http.MethodGet,
		"/api/v1/search?q=Jarvis&source=jarvis", jarvis.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("search after revoke status = %d, want 401", rec.Code)
	}
}
