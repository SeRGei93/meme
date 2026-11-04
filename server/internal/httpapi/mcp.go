package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/SeRGei93/meme/server/internal/auth"
	"github.com/SeRGei93/meme/server/internal/mcp"
)

const (
	mcpPath         = "/mcp"
	maxMCPBodyBytes = 10 << 20 // docs/mcp-tools.md §7
)

// handleMCP is the Streamable HTTP transport for MCP: one JSON-RPC message per
// POST, JSON responses, stateless sessions. Auth and body limits run before
// JSON-RPC; scope failures and invalid/revoked tokens use the REST envelope.
func (s *server) handleMCP(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type",
			"Content-Type must be application/json", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMCPBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large",
				"JSON-RPC request exceeds 10 MiB", nil)
			return
		}
		writeError(w, r, http.StatusBadRequest, "invalid_request", "could not read request body", nil)
		return
	}

	request, protocolErr := mcp.Parse(body)
	if protocolErr != nil {
		writeJSON(w, http.StatusOK, protocolErr)
		return
	}

	// Notifications never receive a JSON-RPC response.
	if request.IsNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	principal, _ := principalFrom(r.Context())
	source, ok := s.knowledgeSource(w, r, principal)
	if !ok {
		return
	}

	if request.Method == mcp.MethodToolsCall {
		toolName, _, callErr := mcp.ParseToolCall(request.Params)
		if callErr != nil {
			writeJSON(w, http.StatusOK, &mcp.Response{JSONRPC: "2.0", ID: request.ID, Error: callErr})
			return
		}
		tool, ok := mcp.LookupTool(toolName)
		if !ok {
			writeJSON(w, http.StatusOK, &mcp.Response{JSONRPC: "2.0", ID: request.ID,
				Error: &mcp.ErrorObject{Code: mcp.CodeInvalidParams, Message: "invalid params: unknown tool " + toolName}})
			return
		}
		if !principal.HasAnyScope([]auth.Scope{tool.Scope}) {
			writeError(w, r, http.StatusForbidden, "forbidden",
				"token does not have a required scope",
				map[string]any{"required_any_of": auth.ScopeStrings([]auth.Scope{tool.Scope})})
			return
		}
	}

	call := mcp.CallContext{
		ClientID:  principal.Client.ID,
		Source:    source,
		RequestID: requestIDFrom(r.Context()),
	}

	result, handleErr := s.mcpServer.Handle(r.Context(), request, call)
	writeJSON(w, http.StatusOK, &mcp.Response{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  result,
		Error:   handleErr,
	})
}

func decodeToolArguments(raw json.RawMessage, target any) *mcp.ToolError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &mcp.ToolError{Code: "validation_failed", Message: "arguments: " + err.Error()}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &mcp.ToolError{Code: "validation_failed", Message: "arguments: unexpected trailing data"}
	}
	return nil
}
