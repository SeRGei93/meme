package card

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/mask"
)

// DefaultLLMTimeout bounds one card request.
const DefaultLLMTimeout = 60 * time.Second

const maxLLMResponseBytes = 1 << 20

// OpenAIOptions configures an OpenAI-compatible chat completions client.
// APIKey may be empty for local endpoints that do not authenticate.
type OpenAIOptions struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTPClient *http.Client
}

// OpenAIClient calls POST <BaseURL>/chat/completions.
type OpenAIClient struct {
	endpoint string
	apiKey   string
	model    string
	http     *http.Client
}

// NewOpenAIClient validates the endpoint and returns a client.
func NewOpenAIClient(opts OpenAIOptions) (*OpenAIClient, error) {
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		return nil, errors.New("llm: base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("llm: invalid base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("llm: base URL must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("llm: base URL must include a host")
	}

	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return nil, errors.New("llm: model is required")
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultLLMTimeout}
	}

	return &OpenAIClient{
		endpoint: strings.TrimRight(baseURL, "/") + "/chat/completions",
		apiKey:   strings.TrimSpace(opts.APIKey),
		model:    model,
		http:     httpClient,
	}, nil
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat responseFormat `json:"response_format"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one chat completion request and returns the assistant text.
func (c *OpenAIClient) Complete(ctx context.Context, messages []Message) (string, error) {
	if c == nil {
		return "", errors.New("llm: client is nil")
	}
	if len(messages) == 0 {
		return "", errors.New("llm: at least one message is required")
	}

	payload, err := json.Marshal(chatRequest{
		Model:          c.model,
		Messages:       messages,
		Temperature:    0,
		ResponseFormat: responseFormat{Type: "json_object"},
	})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("llm: request failed: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxLLMResponseBytes))
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("llm: request failed with status %d: %s",
			response.StatusCode, c.sanitize(providerMessage(body)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("llm: provider error: %s", c.sanitize(parsed.Error.Message))
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: response contains no choices")
	}

	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("llm: response content is empty")
	}
	return content, nil
}

func providerMessage(body []byte) string {
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil && parsed.Error.Message != "" {
		return parsed.Error.Message
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "empty response body"
	}
	const maxErrorRunes = 500
	runes := []rune(message)
	if len(runes) > maxErrorRunes {
		return string(runes[:maxErrorRunes]) + "..."
	}
	return message
}

// sanitize removes the configured key explicitly in addition to the generic
// credential patterns, because a provider may echo it back verbatim.
func (c *OpenAIClient) sanitize(value string) string {
	value = mask.Text(value)
	if c.apiKey != "" {
		value = strings.ReplaceAll(value, c.apiKey, mask.Redacted)
	}
	return value
}
