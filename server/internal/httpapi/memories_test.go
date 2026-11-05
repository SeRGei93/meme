package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type memoryResponseBody struct {
	RecordID int64  `json:"record_id"`
	JobID    *int64 `json:"job_id"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

func newMemoryEnv(t *testing.T) (env *testEnv, jarvisToken, opencodeToken, readToken string) {
	t.Helper()

	env = newTestEnv(t)
	jarvisToken = createClientWithSource(t, env, "jarvis-writer", "jarvis", "jarvis", []string{"ingest"}).Token
	opencodeToken = createClientWithSource(t, env, "opencode-remember", "opencode", "opencode", []string{"ingest"}).Token
	readToken = createClientToken(t, env, "ui-reader", "ui", []string{"ui"})
	return env, jarvisToken, opencodeToken, readToken
}

func doMemory(t *testing.T, env *testEnv, token string, body any, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	return doMemoryRaw(t, env, token, "application/json", mustJSON(t, body), idempotencyKey)
}

func doMemoryRaw(t *testing.T, env *testEnv, token, contentType string, body []byte, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories", bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec
}

func decodeMemoryResponse(t *testing.T, rec *httptest.ResponseRecorder) memoryResponseBody {
	t.Helper()

	var body memoryResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode memory response: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func listMemoryJobs(t *testing.T, env *testEnv) jobListBody {
	t.Helper()
	return decodeJobs(t, doListJobs(t, env, testAdminToken, ""))
}

func memoryCardBody() map[string]any {
	return map[string]any{
		"external_id": "evt_01JABCDEF",
		"project_key": "second-brain",
		"occurred_at": "2026-11-03T13:30:00Z",
		"title":       "Choose SQLite FTS5 over pgvector",
		"summary":     "v1 uses SQLite FTS5 with BM25 and freshness decay.",
		"questions":   []string{"Why not pgvector?"},
		"keywords":    []string{"sqlite", "fts5", "decision"},
		"atoms":       []string{"No vector search in v1."},
		"raw_text":    "Decision: use SQLite FTS5 instead of pgvector.",
		"metadata":    map[string]any{"agent": "jarvis"},
	}
}

func TestCreateMemoryStoresAProvidedCard(t *testing.T) {
	env, jarvisToken, _, readToken := newMemoryEnv(t)

	rec := doMemory(t, env, jarvisToken, memoryCardBody(), "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	result := decodeMemoryResponse(t, rec)
	if result.RecordID <= 0 {
		t.Errorf("record_id = %d, want > 0", result.RecordID)
	}
	if result.JobID != nil {
		t.Errorf("job_id = %v, want null for a provided card", result.JobID)
	}
	if result.Status != "indexed" {
		t.Errorf("status = %q, want indexed", result.Status)
	}
	if result.Replayed {
		t.Error("replayed = true, want false on the first save")
	}

	record := getRecord(t, env, readToken, result.RecordID)
	if record.Source != "jarvis" {
		t.Errorf("record.source = %q, want jarvis (derived from the token)", record.Source)
	}
	if record.Kind != "memory" {
		t.Errorf("record.kind = %q, want memory", record.Kind)
	}
	if record.Title != "Choose SQLite FTS5 over pgvector" {
		t.Errorf("record.title = %q, want the provided title", record.Title)
	}
	if record.Summary != "v1 uses SQLite FTS5 with BM25 and freshness decay." {
		t.Errorf("record.summary = %q, want the provided summary", record.Summary)
	}
	if record.CardStatus != "ok" {
		t.Errorf("record.card_status = %q, want ok", record.CardStatus)
	}
	if !record.IsFinal {
		t.Error("record.is_final = false, want true")
	}

	jobs := listMemoryJobs(t, env)
	if len(jobs.Items) != 0 {
		t.Errorf("jobs = %+v, want no card job when the card is provided", jobs.Items)
	}

	search := doSearch(t, env, readToken, "q=pgvector")
	if search.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200 (body %s)", search.Code, search.Body.String())
	}
	hits := decodeSearch(t, search)
	if hits.Total != 1 || len(hits.Items) != 1 || hits.Items[0].ID != result.RecordID {
		t.Errorf("search(pgvector) = %+v, want record %d", hits, result.RecordID)
	}
}

func TestCreateMemoryQueuesCardGenerationForTextOnly(t *testing.T) {
	env, jarvisToken, _, readToken := newMemoryEnv(t)

	rec := doMemory(t, env, jarvisToken, map[string]any{
		"raw_text": "Remind me that the backup bucket is called meme.",
	}, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	result := decodeMemoryResponse(t, rec)
	if result.JobID == nil {
		t.Fatal("job_id = nil, want a card-generation job")
	}
	if result.Status != "queued" {
		t.Errorf("status = %q, want queued", result.Status)
	}

	jobs := listMemoryJobs(t, env)
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs = %+v, want one card job", jobs.Items)
	}
	if jobs.Items[0].Kind != "memory" {
		t.Errorf("job.kind = %q, want memory", jobs.Items[0].Kind)
	}
	if jobs.Items[0].Status != "queued" {
		t.Errorf("job.status = %q, want queued", jobs.Items[0].Status)
	}
	if jobs.Items[0].RecordID == nil || *jobs.Items[0].RecordID != result.RecordID {
		t.Errorf("job.record_id = %v, want %d", jobs.Items[0].RecordID, result.RecordID)
	}

	record := getRecord(t, env, readToken, result.RecordID)
	if record.CardStatus != "queued" {
		t.Errorf("record.card_status = %q, want queued", record.CardStatus)
	}
	if record.Title != "Remind me that the backup bucket is called meme." {
		t.Errorf("record.title = %q, want a first-line heuristic title", record.Title)
	}
	if record.ProjectKey != "jarvis" {
		t.Errorf("record.project_key = %q, want the default jarvis", record.ProjectKey)
	}

	search := doSearch(t, env, readToken, "q=backup")
	if search.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200 (body %s)", search.Code, search.Body.String())
	}
	hits := decodeSearch(t, search)
	if hits.Total != 1 || len(hits.Items) != 1 || hits.Items[0].ID != result.RecordID {
		t.Errorf("search(backup) = %+v, want record %d", hits, result.RecordID)
	}
}

func TestCreateMemoryIsIdempotentForTheSameContent(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)

	first := doMemory(t, env, jarvisToken, memoryCardBody(), "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save: status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}
	created := decodeMemoryResponse(t, first)

	second := doMemory(t, env, jarvisToken, memoryCardBody(), "")
	if second.Code != http.StatusOK {
		t.Fatalf("second save: status = %d, want 200 (body %s)", second.Code, second.Body.String())
	}
	replay := decodeMemoryResponse(t, second)
	if !replay.Replayed {
		t.Error("second save: replayed = false, want true")
	}
	if replay.RecordID != created.RecordID {
		t.Errorf("second save: record_id = %d, want %d", replay.RecordID, created.RecordID)
	}

	third := doMemory(t, env, jarvisToken, memoryCardBody(), "another-idempotency-key")
	if third.Code != http.StatusOK {
		t.Fatalf("third save: status = %d, want 200 (body %s)", third.Code, third.Body.String())
	}
	naturalReplay := decodeMemoryResponse(t, third)
	if !naturalReplay.Replayed || naturalReplay.RecordID != created.RecordID {
		t.Errorf("third save = %+v, want a replay of record %d", naturalReplay, created.RecordID)
	}
}

func TestCreateMemoryRejectsChangedContentForTheSameExternalID(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)

	first := doMemory(t, env, jarvisToken, memoryCardBody(), "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save: status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}

	changed := memoryCardBody()
	changed["raw_text"] = "Decision: use pgvector instead."
	rec := doMemory(t, env, jarvisToken, changed, "changed-key")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "conflict" {
		t.Errorf("code = %q, want conflict", envelope.Error.Code)
	}
	if envelope.Error.Details["field"] != "external_id" {
		t.Errorf("details = %v, want field external_id", envelope.Error.Details)
	}
}

func TestCreateMemoryRejectsAReusedIdempotencyKeyWithADifferentBody(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)

	first := doMemory(t, env, jarvisToken, memoryCardBody(), "fixed-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save: status = %d, want 201 (body %s)", first.Code, first.Body.String())
	}

	rec := doMemory(t, env, jarvisToken, map[string]any{
		"external_id": "evt_other",
		"raw_text":    "A different memory.",
	}, "fixed-key")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", envelope.Error.Code)
	}
}

func TestCreateMemoryDerivesSourceFromTheToken(t *testing.T) {
	env, jarvisToken, opencodeToken, readToken := newMemoryEnv(t)

	jarvis := decodeMemoryResponse(t, doMemory(t, env, jarvisToken, map[string]any{
		"external_id": "evt_jarvis",
		"title":       "Jarvis fact",
		"summary":     "A fact from Jarvis.",
		"raw_text":    "A fact from Jarvis.",
	}, ""))
	opencode := decodeMemoryResponse(t, doMemory(t, env, opencodeToken, map[string]any{
		"external_id": "evt_opencode",
		"title":       "Remember command fact",
		"summary":     "A fact from /remember.",
		"raw_text":    "A fact from /remember.",
	}, ""))

	if source := getRecord(t, env, readToken, jarvis.RecordID).Source; source != "jarvis" {
		t.Errorf("jarvis token record.source = %q, want jarvis", source)
	}
	if source := getRecord(t, env, readToken, opencode.RecordID).Source; source != "opencode" {
		t.Errorf("opencode token record.source = %q, want opencode", source)
	}
}

func TestCreateMemoryRejectsTokensThatCannotWriteMemories(t *testing.T) {
	env, _, _, _ := newMemoryEnv(t)
	readOnly := createClientToken(t, env, "jarvis-reader", "jarvis", []string{"mcp:read"})
	uiWithIngest := createClientToken(t, env, "ui-writer", "ui", []string{"ingest"})

	tests := []struct {
		name   string
		token  string
		status int
		code   string
	}{
		{"missing token", "", http.StatusUnauthorized, "unauthorized"},
		{"read only", readOnly, http.StatusForbidden, "forbidden"},
		{"ui kind", uiWithIngest, http.StatusForbidden, "forbidden"},
		{"bootstrap admin", testAdminToken, http.StatusForbidden, "forbidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doMemory(t, env, tt.token, memoryCardBody(), "")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != tt.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, tt.code)
			}
		})
	}
}

func TestCreateMemoryValidatesTheRequest(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)
	tooLongTitle := strings.Repeat("t", 501)

	tests := []struct {
		name   string
		body   any
		status int
		code   string
		field  string
	}{
		{
			name:   "missing raw text",
			body:   map[string]any{"external_id": "evt_1"},
			status: http.StatusUnprocessableEntity,
			code:   "validation_failed",
			field:  "raw_text",
		},
		{
			name:   "source in the body",
			body:   map[string]any{"raw_text": "x", "source": "jarvis"},
			status: http.StatusUnprocessableEntity,
			code:   "validation_failed",
			field:  "source",
		},
		{
			name:   "wrong raw_text type",
			body:   map[string]any{"raw_text": 42},
			status: http.StatusUnprocessableEntity,
			code:   "validation_failed",
			field:  "raw_text",
		},
		{
			name:   "title too long",
			body:   map[string]any{"raw_text": "x", "title": tooLongTitle},
			status: http.StatusUnprocessableEntity,
			code:   "validation_failed",
			field:  "title",
		},
		{
			name:   "invalid occurred_at",
			body:   map[string]any{"raw_text": "x", "occurred_at": "not-a-date"},
			status: http.StatusUnprocessableEntity,
			code:   "validation_failed",
			field:  "occurred_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doMemory(t, env, jarvisToken, tt.body, "")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != tt.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, tt.code)
			}
			if envelope.Error.Details["field"] != tt.field {
				t.Errorf("details = %v, want field %q", envelope.Error.Details, tt.field)
			}
		})
	}
}

func TestCreateMemoryRejectsMalformedRequests(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)

	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{"malformed json", "application/json", `{"raw_text":`, http.StatusBadRequest, "invalid_request"},
		{"empty body", "application/json", ``, http.StatusBadRequest, "invalid_request"},
		{"wrong content type", "text/plain", `{"raw_text":"x"}`, http.StatusUnsupportedMediaType, "unsupported_media_type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doMemoryRaw(t, env, jarvisToken, tt.contentType, []byte(tt.body), "")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != tt.code {
				t.Errorf("code = %q, want %q", envelope.Error.Code, tt.code)
			}
		})
	}
}

func TestCreateMemoryRejectsOversizedBody(t *testing.T) {
	env, jarvisToken, _, _ := newMemoryEnv(t)

	rec := doMemory(t, env, jarvisToken, map[string]any{
		"raw_text": "x",
		"title":    strings.Repeat("a", 10<<20),
	}, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Error.Code; code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", code)
	}
}

func TestCreateMemoryMasksSecretsBeforePersistence(t *testing.T) {
	env, jarvisToken, _, readToken := newMemoryEnv(t)

	result := decodeMemoryResponse(t, doMemory(t, env, jarvisToken, map[string]any{
		"external_id": "evt_secret",
		"raw_text":    "The old password was password=hunter2.",
		"metadata":    map[string]any{"note": "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
	}, ""))

	record := getRecord(t, env, readToken, result.RecordID)
	if strings.Contains(record.RawText, "hunter2") {
		t.Errorf("record.raw_text = %q, want the secret masked", record.RawText)
	}
	if note, _ := record.Metadata["note"].(string); strings.Contains(note, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Errorf("record.metadata = %v, want the secret masked", record.Metadata)
	}
}
