package mask_test

import (
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/mask"
)

func TestTextMasksKnownTokenFormats(t *testing.T) {
	secrets := []string{
		"sk-proj-" + strings.Repeat("x", 32),
		"sk-ant-api03-" + strings.Repeat("x", 32),
		"ghp_" + strings.Repeat("x", 36),
		"github_pat_" + strings.Repeat("x", 60),
		"glpat-" + strings.Repeat("x", 20),
		"AKIA" + strings.Repeat("X", 16),
		"AIza" + strings.Repeat("x", 35),
		"xoxb-" + strings.Repeat("0", 20),
		"npm_" + strings.Repeat("x", 36),
		"hf_" + strings.Repeat("x", 30),
		"sk_live_" + strings.Repeat("X", 24),
		"eyJ" + strings.Repeat("A", 10) + "." + strings.Repeat("B", 10) + "." + strings.Repeat("C", 10),
		"sb_opencode_" + strings.Repeat("x", 32),
	}

	for _, secret := range secrets {
		t.Run(secret[:min(len(secret), 12)], func(t *testing.T) {
			input := "before " + secret + " after"
			got := mask.Text(input)

			if strings.Contains(got, secret) {
				t.Fatalf("Text() leaked %q in %q", secret, got)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Fatalf("Text() = %q, want a [REDACTED] placeholder", got)
			}
			if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
				t.Errorf("Text() = %q, want surrounding text preserved", got)
			}
		})
	}
}
