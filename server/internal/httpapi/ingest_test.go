package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type ingestResponseBody struct {
	RecordID int64  `json:"record_id"`
	JobID    *int64 `json:"job_id"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

type recordBody struct {
	ID          int64          `json:"id"`
	Source      string         `json:"source"`
	Kind        string         `json:"kind"`
	ProjectKey  string         `json:"project_key"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Questions   []string       `json:"questions"`
	Keywords    []string       `json:"keywords"`
	Atoms       []string       `json:"atoms"`
	IsFinal     bool           `json:"is_final"`
	StartedAt   *time.Time     `json:"started_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	FinalizedAt *time.Time     `json:"finalized_at"`
	CardStatus  string         `json:"card_status"`
	RawText     string         `json:"raw_text"`
	Revision    int64          `json:"revision"`
	Metadata    map[string]any `json:"metadata"`
	IndexedAt   time.Time      `json:"indexed_at"`
}

func validBundle() map[string]any {
	return map[string]any{
		"bundle_id":    "6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		"external_id":  "ses_01JABCDEF",
		"project_key":  "github.com/SeRGei93/meme",
		"project_path": "/Users/serg/PhpstormProjects/meme",
		"title":        "Fix SQLite migration ordering",
		"final":        true,
		"started_at":   "2026-11-03T12:00:00Z",
		"updated_at":   "2026-11-03T13:00:00Z",
		"messages": []any{
			map[string]any{
				"id":   "msg_1",
				"role": "user",
				"text": "Why does migration 004 run before 003?",
				"at":   "2026-11-03T12:00:10Z",
			},
			map[string]any{
				"id":   "msg_2",
				"role": "assistant",
				"text": "The runner sorts filenames lexicographically.",
				"at":   "2026-11-03T12:00:30Z",
			},
		},
		"metadata": map[string]any{
			"agent":            "build",
			"model":            "provider/model",
			"branch":           "main",
			"opencode_version": "1.2.3",
		},
	}
}

func cloneBundle(t *testing.T, bundle map[string]any) map[string]any {
	t.Helper()
	raw := mustJSON(t, bundle)
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatalf("clone bundle: %v", err)
	}
	return clone
}

func doIngest(t *testing.T, env *testEnv, token string, body any, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ingest", bytes.NewReader(mustJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
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

func decodeIngestResponse(t *testing.T, rec *httptest.ResponseRecorder) ingestResponseBody {
	t.Helper()
	var body ingestResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ingest response: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func getRecord(t *testing.T, env *testEnv, token string, id int64) recordBody {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/v1/records/"+strconv.FormatInt(id, 10), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET record %d: status = %d, want 200 (body %s)", id, rec.Code, rec.Body.String())
	}
	var body recordBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode record: %v (body %q)", err, rec.Body.String())
	}
	return body
}

func newIngestEnv(t *testing.T) (*testEnv, string, string) {
	t.Helper()
	env := newTestEnv(t)
	ingestToken := createClientToken(t, env, "opencode-capture", "opencode", []string{"ingest"})
	readToken := createClientToken(t, env, "ui-reader", "ui", []string{"ui"})
	return env, ingestToken, readToken
}

func TestIngestAcceptsBundleAndServesRecord(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	rec := doIngest(t, env, ingestToken, validBundle(), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	accepted := decodeIngestResponse(t, rec)
	if accepted.RecordID <= 0 {
		t.Errorf("record_id = %d, want > 0", accepted.RecordID)
	}
	if accepted.JobID == nil || *accepted.JobID <= 0 {
		t.Errorf("job_id = %v, want a queued job id", accepted.JobID)
	}
	if accepted.Status != "queued" {
		t.Errorf("status = %q, want queued", accepted.Status)
	}
	if accepted.Replayed {
		t.Error("replayed = true, want false on first ingest")
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.ID != accepted.RecordID {
		t.Errorf("record.id = %d, want %d", record.ID, accepted.RecordID)
	}
	if record.Source != "opencode" {
		t.Errorf("record.source = %q, want opencode (derived from token)", record.Source)
	}
	if record.Kind != "session" {
		t.Errorf("record.kind = %q, want session", record.Kind)
	}
	if record.ProjectKey != "github.com/SeRGei93/meme" {
		t.Errorf("record.project_key = %q, want the bundle project key", record.ProjectKey)
	}
	if record.Title != "Fix SQLite migration ordering" {
		t.Errorf("record.title = %q, want the bundle title", record.Title)
	}
	if !strings.Contains(record.Summary, "lexicographically") {
		t.Errorf("record.summary = %q, want a heuristic summary from the assistant reply", record.Summary)
	}
	if record.RawText != "user: Why does migration 004 run before 003?\n\nassistant: The runner sorts filenames lexicographically." {
		t.Errorf("record.raw_text = %q, want role-prefixed message text", record.RawText)
	}
	if record.Revision != 1 {
		t.Errorf("record.revision = %d, want 1", record.Revision)
	}
	if !record.IsFinal {
		t.Error("record.is_final = false, want true")
	}
	if record.FinalizedAt == nil {
		t.Error("record.finalized_at = nil, want a timestamp")
	}
	if record.CardStatus != "queued" {
		t.Errorf("record.card_status = %q, want queued", record.CardStatus)
	}
	if record.Metadata["model"] != "provider/model" || record.Metadata["branch"] != "main" {
		t.Errorf("record.metadata = %v, want the bundle metadata", record.Metadata)
	}
	if record.Questions == nil || record.Keywords == nil || record.Atoms == nil {
		t.Errorf("record card lists = (%v, %v, %v), want empty arrays not null", record.Questions, record.Keywords, record.Atoms)
	}
}

func TestIngestIsIdempotentForUnchangedContent(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)
	bundle := validBundle()

	first := doIngest(t, env, ingestToken, bundle, "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	accepted := decodeIngestResponse(t, first)

	second := doIngest(t, env, ingestToken, bundle, "")
	if second.Code != http.StatusOK {
		t.Fatalf("second ingest: status = %d, want 200 (body %s)", second.Code, second.Body.String())
	}
	replay := decodeIngestResponse(t, second)
	if !replay.Replayed {
		t.Error("second ingest: replayed = false, want true")
	}
	if replay.RecordID != accepted.RecordID {
		t.Errorf("second ingest: record_id = %d, want %d", replay.RecordID, accepted.RecordID)
	}
	if replay.JobID == nil || accepted.JobID == nil || *replay.JobID != *accepted.JobID {
		t.Errorf("second ingest: job_id = %v, want the original %v", replay.JobID, accepted.JobID)
	}
	if replay.Status != "queued" {
		t.Errorf("second ingest: status = %q, want queued", replay.Status)
	}

	third := doIngest(t, env, ingestToken, bundle, "another-idempotency-key")
	if third.Code != http.StatusOK {
		t.Fatalf("third ingest: status = %d, want 200 (body %s)", third.Code, third.Body.String())
	}
	naturalReplay := decodeIngestResponse(t, third)
	if !naturalReplay.Replayed || naturalReplay.RecordID != accepted.RecordID {
		t.Errorf("third ingest = %+v, want a replay of record %d", naturalReplay, accepted.RecordID)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Revision != 1 {
		t.Errorf("record.revision = %d, want 1 after replays", record.Revision)
	}
}

func TestIngestChangedContentUpdatesRecordAndRevision(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)
	firstBundle := validBundle()

	first := doIngest(t, env, ingestToken, firstBundle, "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	accepted := decodeIngestResponse(t, first)

	updated := cloneBundle(t, firstBundle)
	updated["bundle_id"] = "7a2b3c4d-6e7f-4a1b-8c9d-0e1f2a3b4c5d"
	updated["updated_at"] = "2026-11-03T14:00:00Z"
	updated["messages"] = []any{
		map[string]any{
			"id":   "msg_1",
			"role": "user",
			"text": "Why does migration 004 run before 003?",
			"at":   "2026-11-03T12:00:10Z",
		},
		map[string]any{
			"id":   "msg_2",
			"role": "assistant",
			"text": "The migration runner now sorts by numeric prefix.",
			"at":   "2026-11-03T14:00:00Z",
		},
	}

	second := doIngest(t, env, ingestToken, updated, "")
	if second.Code != http.StatusAccepted {
		t.Fatalf("second ingest: status = %d, want 202 (body %s)", second.Code, second.Body.String())
	}
	revision := decodeIngestResponse(t, second)
	if revision.Replayed {
		t.Error("second ingest: replayed = true, want false for changed content")
	}
	if revision.RecordID != accepted.RecordID {
		t.Errorf("second ingest: record_id = %d, want the existing %d", revision.RecordID, accepted.RecordID)
	}
	if revision.JobID == nil || accepted.JobID == nil || *revision.JobID == *accepted.JobID {
		t.Errorf("second ingest: job_id = %v, want a new job (first was %v)", revision.JobID, accepted.JobID)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Revision != 2 {
		t.Errorf("record.revision = %d, want 2", record.Revision)
	}
	if !strings.Contains(record.RawText, "sorts by numeric prefix") || strings.Contains(record.RawText, "lexicographically") {
		t.Errorf("record.raw_text = %q, want the updated message text", record.RawText)
	}
	if !record.UpdatedAt.Equal(time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)) {
		t.Errorf("record.updated_at = %s, want the newer bundle timestamp", record.UpdatedAt)
	}
	if !strings.Contains(record.Summary, "numeric prefix") {
		t.Errorf("record.summary = %q, want the updated heuristic summary", record.Summary)
	}
}

func TestIngestNonFinalStoresWithoutJob(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)
	bundle := validBundle()
	bundle["final"] = false

	rec := doIngest(t, env, ingestToken, bundle, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	accepted := decodeIngestResponse(t, rec)
	if accepted.JobID != nil {
		t.Errorf("job_id = %v, want null for final=false", accepted.JobID)
	}
	if accepted.Status != "stored" {
		t.Errorf("status = %q, want stored", accepted.Status)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.IsFinal {
		t.Error("record.is_final = true, want false")
	}
	if record.FinalizedAt != nil {
		t.Errorf("record.finalized_at = %v, want nil", record.FinalizedAt)
	}
	if record.CardStatus != "heuristic" {
		t.Errorf("record.card_status = %q, want heuristic", record.CardStatus)
	}
}

func TestIngestNonFinalUpdateKeepsExistingFinalCard(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	first := doIngest(t, env, ingestToken, validBundle(), "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	accepted := decodeIngestResponse(t, first)

	live := cloneBundle(t, validBundle())
	live["bundle_id"] = "8c7d6e5f-4a3b-2c1d-0e9f-8a7b6c5d4e3f"
	live["final"] = false
	live["updated_at"] = "2026-11-03T14:00:00Z"
	live["messages"] = []any{
		map[string]any{
			"id":   "msg_1",
			"role": "user",
			"text": "Why does migration 004 run before 003?",
			"at":   "2026-11-03T12:00:10Z",
		},
		map[string]any{
			"id":   "msg_2",
			"role": "assistant",
			"text": "A live capture with more context.",
			"at":   "2026-11-03T14:00:00Z",
		},
	}

	update := doIngest(t, env, ingestToken, live, "")
	if update.Code != http.StatusAccepted {
		t.Fatalf("live ingest: status = %d, want 202 (body %s)", update.Code, update.Body.String())
	}
	result := decodeIngestResponse(t, update)
	if result.JobID != nil || result.Status != "stored" {
		t.Errorf("live ingest = %+v, want stored without a job", result)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Revision != 2 {
		t.Errorf("record.revision = %d, want 2", record.Revision)
	}
	if !strings.Contains(record.RawText, "live capture with more context") {
		t.Errorf("record.raw_text = %q, want the updated live text", record.RawText)
	}
	if !record.IsFinal {
		t.Error("record.is_final = false, want the final state preserved")
	}
	if record.FinalizedAt == nil {
		t.Error("record.finalized_at = nil, want the finalized timestamp preserved")
	}
	if record.CardStatus != "queued" {
		t.Errorf("record.card_status = %q, want the queued card preserved", record.CardStatus)
	}
	if strings.Contains(record.Summary, "live capture") {
		t.Errorf("record.summary = %q, want the existing card summary preserved", record.Summary)
	}
}

func TestIngestIgnoresStaleBundle(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	first := doIngest(t, env, ingestToken, validBundle(), "")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	accepted := decodeIngestResponse(t, first)

	stale := cloneBundle(t, validBundle())
	stale["bundle_id"] = "1d2e3f4a-5b6c-7d8e-9f0a-1b2c3d4e5f6a"
	stale["updated_at"] = "2026-11-03T12:30:00Z"
	stale["messages"] = []any{
		map[string]any{
			"id":   "msg_1",
			"role": "user",
			"text": "A stale question",
			"at":   "2026-11-03T12:00:10Z",
		},
		map[string]any{
			"id":   "msg_2",
			"role": "assistant",
			"text": "A stale answer that must not win.",
			"at":   "2026-11-03T12:30:00Z",
		},
	}

	rec := doIngest(t, env, ingestToken, stale, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stale ingest: status = %d, want 200 replay (body %s)", rec.Code, rec.Body.String())
	}
	result := decodeIngestResponse(t, rec)
	if !result.Replayed {
		t.Error("stale ingest: replayed = false, want true")
	}
	if result.RecordID != accepted.RecordID {
		t.Errorf("stale ingest: record_id = %d, want %d", result.RecordID, accepted.RecordID)
	}
	if result.JobID == nil || accepted.JobID == nil || *result.JobID != *accepted.JobID {
		t.Errorf("stale ingest: job_id = %v, want the existing %v", result.JobID, accepted.JobID)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Revision != 1 {
		t.Errorf("record.revision = %d, want 1 (stale bundle ignored)", record.Revision)
	}
	if strings.Contains(record.RawText, "stale") {
		t.Errorf("record.raw_text = %q, want the newer content", record.RawText)
	}
	if !record.UpdatedAt.Equal(time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("record.updated_at = %s, want the newer timestamp", record.UpdatedAt)
	}
}

func TestIngestRejectsIdempotencyKeyReuseWithDifferentBody(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)
	bundle := validBundle()

	first := doIngest(t, env, ingestToken, bundle, "shared-key")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", first.Code, first.Body.String())
	}
	accepted := decodeIngestResponse(t, first)

	changed := cloneBundle(t, bundle)
	changed["bundle_id"] = "9b8c7d6e-5f4a-3b2c-1d0e-9f8a7b6c5d4e"
	changed["title"] = "A different payload"
	second := doIngest(t, env, ingestToken, changed, "shared-key")
	if second.Code != http.StatusConflict {
		t.Fatalf("second ingest: status = %d, want 409 (body %s)", second.Code, second.Body.String())
	}
	if code := decodeError(t, second).Error.Code; code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", code)
	}

	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Revision != 1 || record.Title != "Fix SQLite migration ordering" {
		t.Errorf("record = %+v, want the original record unchanged", record)
	}
}

func TestIngestRejectsInvalidIdempotencyKey(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)

	rec := doIngest(t, env, ingestToken, validBundle(), strings.Repeat("k", 129))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	envelope := decodeError(t, rec)
	if envelope.Error.Code != "invalid_request" {
		t.Errorf("code = %q, want invalid_request", envelope.Error.Code)
	}
	if field := envelope.Error.Details["field"]; field != "Idempotency-Key" {
		t.Errorf("details.field = %v, want Idempotency-Key", field)
	}
}

func TestIngestValidatesBundle(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)

	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantField string
	}{
		{
			name:      "missing final",
			mutate:    func(bundle map[string]any) { delete(bundle, "final") },
			wantField: "final",
		},
		{
			name:      "source is server owned",
			mutate:    func(bundle map[string]any) { bundle["source"] = "opencode" },
			wantField: "source",
		},
		{
			name:      "empty messages",
			mutate:    func(bundle map[string]any) { bundle["messages"] = []any{} },
			wantField: "messages",
		},
		{
			name: "invalid role",
			mutate: func(bundle map[string]any) {
				bundle["messages"].([]any)[0].(map[string]any)["role"] = "robot"
			},
			wantField: "messages[0].role",
		},
		{
			name: "missing message text",
			mutate: func(bundle map[string]any) {
				delete(bundle["messages"].([]any)[0].(map[string]any), "text")
			},
			wantField: "messages[0].text",
		},
		{
			name:      "empty title",
			mutate:    func(bundle map[string]any) { bundle["title"] = "" },
			wantField: "title",
		},
		{
			name:      "title too long",
			mutate:    func(bundle map[string]any) { bundle["title"] = strings.Repeat("a", 501) },
			wantField: "title",
		},
		{
			name:      "missing metadata",
			mutate:    func(bundle map[string]any) { delete(bundle, "metadata") },
			wantField: "metadata",
		},
		{
			name: "unknown metadata key",
			mutate: func(bundle map[string]any) {
				bundle["metadata"].(map[string]any)["secret"] = "value"
			},
			wantField: "metadata.secret",
		},
		{
			name: "started after updated",
			mutate: func(bundle map[string]any) {
				bundle["started_at"] = "2026-11-03T15:00:00Z"
				bundle["updated_at"] = "2026-11-03T14:00:00Z"
			},
			wantField: "started_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := cloneBundle(t, validBundle())
			tt.mutate(bundle)

			rec := doIngest(t, env, ingestToken, bundle, "")
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %s)", rec.Code, rec.Body.String())
			}
			envelope := decodeError(t, rec)
			if envelope.Error.Code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", envelope.Error.Code)
			}
			if field := envelope.Error.Details["field"]; field != tt.wantField {
				t.Errorf("details.field = %v, want %q", field, tt.wantField)
			}
		})
	}
}

func TestIngestRejectsMalformedRequests(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)

	tests := []struct {
		name        string
		body        []byte
		contentType string
		wantStatus  int
		wantCode    string
	}{
		{
			name:        "malformed json",
			body:        []byte(`{"bundle_id":`),
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
		},
		{
			name:        "empty body",
			body:        nil,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
		},
		{
			name:        "trailing json",
			body:        append(mustJSON(t, validBundle()), []byte(`{}`)...),
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
			wantCode:    "invalid_request",
		},
		{
			name:        "wrong field type",
			body:        []byte(`{"bundle_id":1}`),
			contentType: "application/json",
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    "validation_failed",
		},
		{
			name:        "wrong content type",
			body:        mustJSON(t, validBundle()),
			contentType: "text/plain",
			wantStatus:  http.StatusUnsupportedMediaType,
			wantCode:    "unsupported_media_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.doRaw(t, http.MethodPost, "/api/v1/ingest", ingestToken, tt.contentType, bytes.NewReader(tt.body))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if code := decodeError(t, rec).Error.Code; code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestIngestRequiresOpenCodeClientAndIngestScope(t *testing.T) {
	env := newTestEnv(t)
	jarvisToken := createClientToken(t, env, "jarvis", "jarvis", []string{"ingest"})
	uiToken := createClientToken(t, env, "ui-with-ingest", "ui", []string{"ingest"})
	opencodeWithoutIngest := createClientToken(t, env, "opencode-reader", "opencode", []string{"mcp:read"})

	tests := []struct {
		name   string
		token  string
		status int
	}{
		{"jarvis cannot ingest sessions", jarvisToken, http.StatusForbidden},
		{"ui cannot ingest sessions", uiToken, http.StatusForbidden},
		{"opencode needs ingest scope", opencodeWithoutIngest, http.StatusForbidden},
		{"bootstrap admin has no source", testAdminToken, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doIngest(t, env, tt.token, validBundle(), "")
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.status, rec.Body.String())
			}
			if code := decodeError(t, rec).Error.Code; code != "forbidden" {
				t.Errorf("code = %q, want forbidden", code)
			}
		})
	}
}

func TestIngestRejectsOversizedBody(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)
	bundle := validBundle()
	bundle["title"] = strings.Repeat("a", 10<<20)

	rec := doIngest(t, env, ingestToken, bundle, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Error.Code; code != "payload_too_large" {
		t.Errorf("code = %q, want payload_too_large", code)
	}
}

func TestGetRecordRequiresReadScopeAndRejectsBadIDs(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)

	accepted := decodeIngestResponse(t, doIngest(t, env, ingestToken, validBundle(), ""))

	rec := env.do(t, http.MethodGet, "/api/v1/records/"+strconv.FormatInt(accepted.RecordID, 10), ingestToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ingest token: status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
	details, _ := decodeError(t, rec).Error.Details["required_any_of"].([]any)
	if len(details) != 2 || details[0] != "ui" || details[1] != "mcp:read" {
		t.Errorf("details.required_any_of = %v, want [ui mcp:read]", details)
	}

	mcpToken := createClientToken(t, env, "mcp-reader", "opencode", []string{"mcp:read"})
	getRecord(t, env, mcpToken, accepted.RecordID)
	getRecord(t, env, readToken, accepted.RecordID)

	rec = env.do(t, http.MethodGet, "/api/v1/records/999999", readToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown record: status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
	if code := decodeError(t, rec).Error.Code; code != "not_found" {
		t.Errorf("unknown record: code = %q, want not_found", code)
	}

	for _, path := range []string{"/api/v1/records/not-a-number", "/api/v1/records/0", "/api/v1/records/-1"} {
		rec = env.do(t, http.MethodGet, path, readToken, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400 (body %s)", path, rec.Code, rec.Body.String())
			continue
		}
		envelope := decodeError(t, rec)
		if envelope.Error.Code != "invalid_request" {
			t.Errorf("GET %s: code = %q, want invalid_request", path, envelope.Error.Code)
		}
		if field := envelope.Error.Details["field"]; field != "id" {
			t.Errorf("GET %s: details.field = %v, want id", path, field)
		}
	}

	rec = env.do(t, http.MethodGet, "/api/v1/records/", readToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET collection path: status = %d, want 404", rec.Code)
	}
}

func TestIngestUsesClientSourceNotRequestField(t *testing.T) {
	env := newTestEnv(t)
	ingestToken := createClientToken(t, env, "opencode-capture", "opencode", []string{"ingest"})
	readToken := createClientToken(t, env, "reader", "ui", []string{"ui"})

	accepted := decodeIngestResponse(t, doIngest(t, env, ingestToken, validBundle(), ""))
	record := getRecord(t, env, readToken, accepted.RecordID)
	if record.Source != "opencode" {
		t.Errorf("record.source = %q, want opencode", record.Source)
	}
}
