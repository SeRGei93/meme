package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/SeRGei93/meme/server/internal/auth"
)

// clientCommand issues a client token through the admin API. It never opens the
// SQLite database: the server is the only writer, and this command works from
// an operator machine or a container shell.
func clientCommand(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("usage: brain client create --name <name> [--source <slug>] [--scopes ingest,mcp:read,mcp:write] [--url https://...]")
	}

	flags := flag.NewFlagSet("client create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	baseURL := flags.String("url", envOr("BRAIN_URL", "http://127.0.0.1:8080"), "server base URL")
	adminToken := flags.String("token", os.Getenv("BRAIN_ADMIN_TOKEN"), "admin token (defaults to BRAIN_ADMIN_TOKEN)")
	name := flags.String("name", "", "client name")
	kind := flags.String("kind", string(auth.KindAgent), "client kind: agent, ui or admin")
	source := flags.String("source", "", "source label (agents only; defaults to a slug of the name)")
	scopes := flags.String("scopes", "", "comma-separated scopes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	if strings.TrimSpace(*adminToken) == "" {
		return fmt.Errorf("an admin token is required: pass --token or set BRAIN_ADMIN_TOKEN")
	}
	parsedScopes, err := parseScopes(*scopes)
	if err != nil {
		return err
	}
	endpoint, err := clientEndpoint(*baseURL)
	if err != nil {
		return err
	}

	request := clientRequest{
		Name:   strings.TrimSpace(*name),
		Kind:   auth.Kind(strings.TrimSpace(*kind)),
		Source: strings.TrimSpace(*source),
		Scopes: parsedScopes,
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+*adminToken)
	req.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("create client: %s returned %d: %s",
			*baseURL, response.StatusCode, strings.TrimSpace(string(payload)))
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, payload, "", "  "); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	fmt.Fprintln(os.Stdout, pretty.String())
	return nil
}

type clientRequest struct {
	Name   string       `json:"name"`
	Kind   auth.Kind    `json:"kind"`
	Source string       `json:"source,omitempty"`
	Scopes []auth.Scope `json:"scopes"`
}

func parseScopes(raw string) ([]auth.Scope, error) {
	parts := strings.Split(raw, ",")
	scopes := make([]auth.Scope, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		scope := auth.Scope(value)
		if !scope.Valid() {
			return nil, fmt.Errorf("unknown scope %q", value)
		}
		scopes = append(scopes, scope)
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("--scopes is required (for example ingest,mcp:read,mcp:write)")
	}
	return scopes, nil
}

func clientEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("--url must be an absolute http(s) URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("--url must not contain a query or fragment")
	}
	switch parsed.Scheme {
	case "https":
		return strings.TrimRight(parsed.String(), "/") + "/api/v1/clients", nil
	case "http":
		if !loopbackHost(parsed.Hostname()) {
			return "", fmt.Errorf("--url must use https unless it points at localhost")
		}
		return strings.TrimRight(parsed.String(), "/") + "/api/v1/clients", nil
	default:
		return "", fmt.Errorf("--url must use http or https")
	}
}

func loopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
