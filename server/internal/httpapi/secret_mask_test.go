package httpapi_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIngestMasksSecretsBeforeStoring(t *testing.T) {
	env, ingestToken, readToken := newIngestEnv(t)
	bundle := validBundle()
	bundle["title"] = "Rotate sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv"
	bundle["messages"] = []any{
		map[string]any{
			"id":   "msg_1",
			"role": "user",
			"text": "My password=hunter2. The database URL is postgres://app:supersecret@db:5432/name",
			"at":   "2026-11-03T12:00:10Z",
		},
		map[string]any{
			"id":   "msg_2",
			"role": "assistant",
			"text": "Set token=abc123 and send Authorization: Bearer abcdef0123456789ABCDEF",
			"at":   "2026-11-03T12:00:30Z",
		},
	}
	bundle["metadata"].(map[string]any)["branch"] = "token=abc123"

	rec := doIngest(t, env, ingestToken, bundle, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	accepted := decodeIngestResponse(t, rec)

	record := getRecord(t, env, readToken, accepted.RecordID)
	rendered := strings.Join([]string{
		record.Title,
		record.Summary,
		record.RawText,
		fmt.Sprint(record.Metadata),
	}, "\n")

	for _, secret := range []string{
		"sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv",
		"hunter2",
		"supersecret",
		"abc123",
		"abcdef0123456789ABCDEF",
	} {
		if strings.Contains(rendered, secret) {
			t.Errorf("stored record leaked %q:\n%s", secret, rendered)
		}
	}
	if !strings.Contains(rendered, "[REDACTED]") {
		t.Errorf("stored record = %q, want redaction placeholders", rendered)
	}
}

func TestIngestRejectsIdempotencyKeyReuseWhenOnlySecretChanged(t *testing.T) {
	env, ingestToken, _ := newIngestEnv(t)
	first := validBundle()
	first["messages"].([]any)[0].(map[string]any)["text"] = "My password=hunter2"
	second := cloneBundle(t, first)
	second["messages"].([]any)[0].(map[string]any)["text"] = "My password=another-secret"

	const key = "shared-secret-key"
	rec := doIngest(t, env, ingestToken, first, key)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first ingest: status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}

	reused := doIngest(t, env, ingestToken, second, key)
	if reused.Code != http.StatusConflict {
		t.Fatalf("second ingest: status = %d, want 409 (body %s)", reused.Code, reused.Body.String())
	}
	if code := decodeError(t, reused).Error.Code; code != "idempotency_key_reused" {
		t.Errorf("code = %q, want idempotency_key_reused", code)
	}
}
