package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "default address",
			env:  map[string]string{},
			want: "http://127.0.0.1:8080/health",
		},
		{
			name: "wildcard bind address maps to loopback",
			env:  map[string]string{"BRAIN_ADDR": "0.0.0.0:9000"},
			want: "http://127.0.0.1:9000/health",
		},
		{
			name: "empty host maps to loopback",
			env:  map[string]string{"BRAIN_ADDR": ":9000"},
			want: "http://127.0.0.1:9000/health",
		},
		{
			name: "loopback address is kept",
			env:  map[string]string{"BRAIN_ADDR": "127.0.0.1:8081"},
			want: "http://127.0.0.1:8081/health",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := healthcheckURL(getenv); got != tt.want {
				t.Errorf("healthcheckURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run([]string{"unknown"}); err == nil {
		t.Fatal("run() error = nil, want error")
	}
}

func TestClientCreatePostsToAdminAPI(t *testing.T) {
	var (
		gotMethod string
		gotAuth   string
		gotBody   clientRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/clients" {
			t.Errorf("path = %q, want /api/v1/clients", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client":{"id":7,"name":"claude","kind":"agent","source":"claude-prod","scopes":["mcp:read"],"created_at":"2026-11-05T12:00:00Z","last_seen_at":null,"revoked_at":null},"token":"sb_agent_secret"}`))
	}))
	defer server.Close()

	err := run([]string{
		"client", "create",
		"--url", server.URL,
		"--token", "admin-token",
		"--name", "claude",
		"--source", "claude-prod",
		"--scopes", "mcp:read",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer admin-token" {
		t.Errorf("Authorization = %q, want Bearer admin-token", gotAuth)
	}
	if gotBody.Name != "claude" || gotBody.Source != "claude-prod" || string(gotBody.Kind) != "agent" {
		t.Errorf("body = %+v, want claude/claude-prod/agent", gotBody)
	}
	if len(gotBody.Scopes) != 1 || gotBody.Scopes[0] != "mcp:read" {
		t.Errorf("scopes = %v, want [mcp:read]", gotBody.Scopes)
	}
}

func TestClientCreateReportsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"client source is already in use"}}`))
	}))
	defer server.Close()

	err := run([]string{
		"client", "create",
		"--url", server.URL,
		"--token", "admin-token",
		"--name", "claude",
		"--scopes", "mcp:read",
	})
	if err == nil || !strings.Contains(err.Error(), "409") {
		t.Fatalf("run() error = %v, want a 409 error", err)
	}
}

func TestClientCreateValidatesInput(t *testing.T) {
	tests := [][]string{
		{"client"},
		{"client", "create", "--name", "claude"},
		{"client", "create", "--name", "claude", "--scopes", "root"},
		{"client", "create", "--name", "claude", "--scopes", "mcp:read", "--url", "http://example.com"},
	}
	for _, args := range tests {
		if err := run(args); err == nil {
			t.Errorf("run(%v) error = nil, want error", args)
		}
	}
}

func TestClientEndpoint(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{"https://meme.aimerce.by", "https://meme.aimerce.by/api/v1/clients", false},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/api/v1/clients", false},
		{"http://localhost:8080/", "http://localhost:8080/api/v1/clients", false},
		{"http://example.com", "", true},
		{"https://example.com?x=1", "", true},
		{"ftp://example.com", "", true},
	}
	for _, tt := range tests {
		got, err := clientEndpoint(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("clientEndpoint(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("clientEndpoint(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
