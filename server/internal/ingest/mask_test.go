package ingest_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/ingest"
)

func TestBundleMaskedMasksAllContentFields(t *testing.T) {
	bundle := ingest.Bundle{
		BundleID:    "6f1a2f4a-5b1c-4c55-9dbb-59f8c0f1a2b3",
		ExternalID:  "ses_01JABCDEF",
		ProjectKey:  "github.com/SeRGei93/meme",
		ProjectPath: "/Users/serg/PhpstormProjects/meme",
		Title:       "Debug sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv",
		Messages: []ingest.Message{
			{
				ID:   "msg_1",
				Role: ingest.RoleUser,
				Text: "My password=hunter2, please help",
				ToolCalls: []ingest.ToolCall{
					{
						Name:    "bash",
						Command: "curl -H 'Authorization: Bearer abcdef0123456789ABCDEF' https://example.com",
						Path:    "token=abc123",
					},
				},
			},
			{
				ID:   "msg_2",
				Role: ingest.RoleAssistant,
				Text: "Use PostgreSQL at postgres://app:supersecret@db:5432/name",
			},
		},
		Metadata: ingest.Metadata{
			"agent":  "build",
			"branch": "password=hunter2",
		},
	}

	masked := bundle.Masked()

	secrets := []string{
		"sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv",
		"hunter2",
		"abcdef0123456789ABCDEF",
		"abc123",
		"supersecret",
	}
	for _, secret := range secrets {
		if strings.Contains(fmt.Sprintf("%+v", masked), secret) {
			t.Errorf("Masked() still contains %q: %+v", secret, masked)
		}
	}

	if masked.BundleID != bundle.BundleID ||
		masked.ExternalID != bundle.ExternalID ||
		masked.ProjectKey != bundle.ProjectKey ||
		masked.ProjectPath != bundle.ProjectPath {
		t.Errorf("Masked() changed identifiers: %+v", masked)
	}
	if masked.Metadata["agent"] != "build" {
		t.Errorf("Masked() changed non-secret metadata: %v", masked.Metadata)
	}

	if !strings.Contains(bundle.Messages[0].Text, "hunter2") {
		t.Error("Masked() mutated the original bundle")
	}
}

func TestBundleMaskedCardUsesMaskedText(t *testing.T) {
	bundle := ingest.Bundle{
		Title: "Fix login",
		Messages: []ingest.Message{
			{
				ID:   "msg_1",
				Role: ingest.RoleAssistant,
				Text: "Set OPENAI_API_KEY=sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv and retry.",
			},
		},
	}

	card := bundle.HeuristicCard()
	if strings.Contains(card.Summary, "sk-proj-AbCdEf0123456789GhIjKlMnOpQrStUv") {
		t.Fatalf("HeuristicCard() leaked a secret: %q", card.Summary)
	}
	if !strings.Contains(card.Summary, "[REDACTED]") {
		t.Errorf("HeuristicCard() = %q, want the redaction placeholder", card.Summary)
	}
}

func TestBundleRawTextMasksSecrets(t *testing.T) {
	bundle := ingest.Bundle{
		Messages: []ingest.Message{
			{
				ID:   "msg_1",
				Role: ingest.RoleUser,
				Text: "Authorization: Bearer abcdef0123456789ABCDEF",
			},
		},
	}

	raw := bundle.RawText()
	if strings.Contains(raw, "abcdef0123456789ABCDEF") {
		t.Fatalf("RawText() leaked a secret: %q", raw)
	}
	if !strings.Contains(raw, "[REDACTED]") {
		t.Errorf("RawText() = %q, want the redaction placeholder", raw)
	}
}

func TestBundleHeuristicCardMasksBeforeTruncating(t *testing.T) {
	secret := "sk-proj-" + strings.Repeat("A", 40)
	bundle := ingest.Bundle{
		Messages: []ingest.Message{
			{
				ID:   "msg_1",
				Role: ingest.RoleAssistant,
				Text: strings.Repeat("a", 995) + " " + secret,
			},
		},
	}

	card := bundle.HeuristicCard()
	if strings.Contains(card.Summary, "sk-p") {
		t.Fatalf("HeuristicCard() leaked a truncated secret prefix: %q", card.Summary)
	}
}
