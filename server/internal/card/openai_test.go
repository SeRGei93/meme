package card_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SeRGei93/meme/server/internal/card"
)

func TestOpenAIClientSendsAChatCompletionRequest(t *testing.T) {
	var got struct {
		Path          string
		Authorization string
		ContentType   string
		Model         string
		Messages      []card.Message `json:"messages"`
		Temperature   float64        `json:"temperature"`
		ResponseType  string
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Path = r.URL.Path
		got.Authorization = r.Header.Get("Authorization")
		got.ContentType = r.Header.Get("Content-Type")

		var body struct {
			Model          string         `json:"model"`
			Messages       []card.Message `json:"messages"`
			Temperature    float64        `json:"temperature"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		got.Model = body.Model
		got.Messages = body.Messages
		got.Temperature = body.Temperature
		got.ResponseType = body.ResponseFormat.Type

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"title\":\"generated\"}"}}]}`))
	}))
	defer server.Close()

	client, err := card.NewOpenAIClient(card.OpenAIOptions{
		BaseURL: server.URL + "/v1/",
		APIKey:  "test-secret",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAIClient() error = %v", err)
	}

	content, err := client.Complete(context.Background(), []card.Message{
		{Role: card.RoleSystem, Content: "system"},
		{Role: card.RoleUser, Content: "session"},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if content != `{"title":"generated"}` {
		t.Errorf("Complete() = %q", content)
	}

	if got.Path != "/v1/chat/completions" {
		t.Errorf("request path = %q, want /v1/chat/completions", got.Path)
	}
	if got.Authorization != "Bearer test-secret" {
		t.Errorf("Authorization = %q, want Bearer test-secret", got.Authorization)
	}
	if got.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got.ContentType)
	}
	if got.Model != "test-model" {
		t.Errorf("model = %q, want test-model", got.Model)
	}
	if got.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", got.Temperature)
	}
	if got.ResponseType != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", got.ResponseType)
	}
	if len(got.Messages) != 2 || got.Messages[1].Content != "session" {
		t.Errorf("messages = %+v", got.Messages)
	}
}

func TestOpenAIClientOmitsAuthorizationWithoutAKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value := r.Header.Get("Authorization"); value != "" {
			t.Errorf("Authorization = %q, want empty for a keyless endpoint", value)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()

	client, err := card.NewOpenAIClient(card.OpenAIOptions{BaseURL: server.URL, Model: "local-model"})
	if err != nil {
		t.Fatalf("NewOpenAIClient() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), []card.Message{{Role: card.RoleUser, Content: "x"}}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
}

func TestOpenAIClientReportsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key sk-secret-value"}}`))
	}))
	defer server.Close()

	client, err := card.NewOpenAIClient(card.OpenAIOptions{BaseURL: server.URL, APIKey: "sk-secret-value", Model: "test-model"})
	if err != nil {
		t.Fatalf("NewOpenAIClient() error = %v", err)
	}

	_, err = client.Complete(context.Background(), []card.Message{{Role: card.RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("Complete() error = nil, want provider error")
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("Complete() error = %q, want status and provider message", err)
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Errorf("Complete() error leaked the API key: %q", err)
	}
}

func TestOpenAIClientRejectsAnEmptyResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "no choices", body: `{"choices":[]}`},
		{name: "empty content", body: `{"choices":[{"message":{"content":""}}]}`},
		{name: "not json", body: `not json`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client, err := card.NewOpenAIClient(card.OpenAIOptions{BaseURL: server.URL, Model: "test-model"})
			if err != nil {
				t.Fatalf("NewOpenAIClient() error = %v", err)
			}
			if _, err := client.Complete(context.Background(), []card.Message{{Role: card.RoleUser, Content: "x"}}); err == nil {
				t.Fatal("Complete() error = nil, want error")
			}
		})
	}
}

func TestNewOpenAIClientRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts card.OpenAIOptions
	}{
		{name: "missing base url", opts: card.OpenAIOptions{Model: "test-model"}},
		{name: "invalid base url", opts: card.OpenAIOptions{BaseURL: "://bad", Model: "test-model"}},
		{name: "missing model", opts: card.OpenAIOptions{BaseURL: "https://example.com/v1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := card.NewOpenAIClient(tt.opts); err == nil {
				t.Fatal("NewOpenAIClient() error = nil, want error")
			}
		})
	}
}
