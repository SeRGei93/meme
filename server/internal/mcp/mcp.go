// Package mcp implements the MCP 2025-06-18 JSON-RPC surface: initialization,
// tool discovery and tool calls. Transport concerns (HTTP, auth, body limits)
// live in internal/httpapi; this package only speaks protocol and delegates
// execution to a Backend so every tool reuses the REST services.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/SeRGei93/meme/server/internal/auth"
)

// ProtocolVersion is the MCP revision from docs/mcp-tools.md.
const ProtocolVersion = "2025-06-18"

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Methods handled by the server.
const (
	MethodInitialize = "initialize"
	MethodPing       = "ping"
	MethodToolsList  = "tools/list"
	MethodToolsCall  = "tools/call"
)

// Request is one JSON-RPC request. ID is kept raw so the response echoes the
// client's id exactly (number, string or null).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ErrorObject is a JSON-RPC protocol error.
type ErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Response is a JSON-RPC response. ID is always present: null for parse
// errors, otherwise the echoed request id.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
}

// CallContext carries the authenticated identity into tool execution.
type CallContext struct {
	ClientID  int64
	Source    string
	RequestID string
}

// ToolError is a tool-level failure: it becomes a successful JSON-RPC response
// with isError=true, not a protocol error.
type ToolError struct {
	Code    string
	Message string
}

func (e *ToolError) Error() string { return e.Code + ": " + e.Message }

// Backend executes the five v1 tools. Implementations reuse the REST services
// so MCP responses cannot drift from /api/v1.
type Backend interface {
	Search(ctx context.Context, call CallContext, arguments json.RawMessage) (any, *ToolError)
	GetRecord(ctx context.Context, call CallContext, arguments json.RawMessage) (any, *ToolError)
	Remember(ctx context.Context, call CallContext, arguments json.RawMessage) (any, *ToolError)
	ListProjects(ctx context.Context, call CallContext, arguments json.RawMessage) (any, *ToolError)
	Stats(ctx context.Context, call CallContext, arguments json.RawMessage) (any, *ToolError)
}

// Tool describes one tool from the v1 contract and the scope it requires.
type Tool struct {
	Name    string
	Scope   auth.Scope
	backend func(Backend, context.Context, CallContext, json.RawMessage) (any, *ToolError)
	info    ToolInfo
}

// Tools returns the five contract tools in contract order.
func Tools() []Tool {
	return []Tool{
		{Name: "search", Scope: auth.ScopeMCPRead, backend: Backend.Search, info: toolInfo(searchInfo)},
		{Name: "get_record", Scope: auth.ScopeMCPRead, backend: Backend.GetRecord, info: toolInfo(getRecordInfo)},
		{Name: "remember", Scope: auth.ScopeMCPWrite, backend: Backend.Remember, info: toolInfo(rememberInfo)},
		{Name: "list_projects", Scope: auth.ScopeMCPRead, backend: Backend.ListProjects, info: toolInfo(listProjectsInfo)},
		{Name: "stats", Scope: auth.ScopeMCPRead, backend: Backend.Stats, info: toolInfo(statsInfo)},
	}
}

// LookupTool returns a tool definition by name.
func LookupTool(name string) (Tool, bool) {
	for _, tool := range Tools() {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}

// Server executes JSON-RPC methods against a Backend.
type Server struct {
	version string
	backend Backend
}

// New returns a server that announces the supplied implementation version.
func New(version string, backend Backend) *Server {
	return &Server{version: version, backend: backend}
}

// Parse decodes one request, distinguishing malformed JSON from a valid JSON
// value that is not a JSON-RPC request. A nil error means the request is
// structurally usable; protocol errors carry the response to send.
func Parse(body []byte) (Request, *Response) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return Request{}, errorResponse(CodeParseError, "parse error: empty body")
	}
	if trimmed[0] == '[' {
		return Request{}, errorResponse(CodeInvalidRequest, "invalid request: batch is not supported")
	}

	var req Request
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return Request{}, errorResponse(CodeParseError, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return Request{}, errorResponse(CodeInvalidRequest, "invalid request: jsonrpc must be 2.0 and method is required")
	}
	return req, nil
}

// IsNotification reports whether the request has no id and therefore no
// response must be sent.
func (r Request) IsNotification() bool {
	return len(bytes.TrimSpace(r.ID)) == 0
}

// ParseToolCall extracts the tool name from tools/call params.
func ParseToolCall(params json.RawMessage) (string, json.RawMessage, *ErrorObject) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return "", nil, &ErrorObject{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	if call.Name == "" {
		return "", nil, &ErrorObject{Code: CodeInvalidParams, Message: "invalid params: tool name is required"}
	}
	return call.Name, call.Arguments, nil
}

// Handle executes initialize, ping, tools/list or tools/call.
func (s *Server) Handle(ctx context.Context, req Request, call CallContext) (any, *ErrorObject) {
	switch req.Method {
	case MethodInitialize:
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "second-brain", "version": s.version},
		}, nil
	case MethodPing:
		return map[string]any{}, nil
	case MethodToolsList:
		tools := Tools()
		infos := make([]ToolInfo, len(tools))
		for i, tool := range tools {
			infos[i] = tool.info
		}
		return map[string]any{"tools": infos}, nil
	case MethodToolsCall:
		name, arguments, errObject := ParseToolCall(req.Params)
		if errObject != nil {
			return nil, errObject
		}
		tool, ok := LookupTool(name)
		if !ok {
			return nil, &ErrorObject{Code: CodeInvalidParams, Message: fmt.Sprintf("invalid params: unknown tool %q", name)}
		}
		result, toolErr := tool.backend(s.backend, ctx, call, arguments)
		if toolErr != nil {
			return errorResult(toolErr, call.RequestID), nil
		}
		return successResult(result), nil
	default:
		return nil, &ErrorObject{Code: CodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}
}

// NeededScope returns the scope a tools/call request requires, or false when
// the method does not invoke a tool.
func NeededScope(req Request) (auth.Scope, bool) {
	if req.Method != MethodToolsCall {
		return "", false
	}
	name, _, errObject := ParseToolCall(req.Params)
	if errObject != nil {
		return "", false
	}
	tool, ok := LookupTool(name)
	if !ok {
		return "", false
	}
	return tool.Scope, true
}

type toolResult struct {
	Content           []content `json:"content"`
	StructuredContent any       `json:"structuredContent,omitempty"`
	IsError           bool      `json:"isError,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func successResult(value any) toolResult {
	raw, err := json.Marshal(value)
	if err != nil {
		return errorResult(&ToolError{Code: "internal_error", Message: "failed to encode result"}, "")
	}
	return toolResult{
		Content:           []content{{Type: "text", Text: string(raw)}},
		StructuredContent: value,
	}
}

func errorResult(toolErr *ToolError, requestID string) toolResult {
	body := map[string]any{"code": toolErr.Code, "message": toolErr.Message}
	if requestID != "" {
		body["request_id"] = requestID
	}
	return toolResult{
		Content:           []content{{Type: "text", Text: toolErr.Code + ": " + toolErr.Message}},
		StructuredContent: map[string]any{"error": body},
		IsError:           true,
	}
}

func errorResponse(code int, message string) *Response {
	return &Response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &ErrorObject{Code: code, Message: message}}
}
