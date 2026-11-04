package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SeRGei93/meme/server/internal/mcp"
)

type fakeBackend struct {
	last    string
	lastCtx mcp.CallContext
	result  any
	err     *mcp.ToolError
}

func (f *fakeBackend) record(name string, call mcp.CallContext) {
	f.last = name
	f.lastCtx = call
}

func (f *fakeBackend) Search(_ context.Context, call mcp.CallContext, _ json.RawMessage) (any, *mcp.ToolError) {
	f.record("search", call)
	return f.result, f.err
}

func (f *fakeBackend) GetRecord(_ context.Context, call mcp.CallContext, _ json.RawMessage) (any, *mcp.ToolError) {
	f.record("get_record", call)
	return f.result, f.err
}

func (f *fakeBackend) Remember(_ context.Context, call mcp.CallContext, _ json.RawMessage) (any, *mcp.ToolError) {
	f.record("remember", call)
	return f.result, f.err
}

func (f *fakeBackend) ListProjects(_ context.Context, call mcp.CallContext, _ json.RawMessage) (any, *mcp.ToolError) {
	f.record("list_projects", call)
	return f.result, f.err
}

func (f *fakeBackend) Stats(_ context.Context, call mcp.CallContext, _ json.RawMessage) (any, *mcp.ToolError) {
	f.record("stats", call)
	return f.result, f.err
}

func decode(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestParseRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{"empty", ``, mcp.CodeParseError},
		{"invalid json", `{`, mcp.CodeParseError},
		{"batch", `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, mcp.CodeInvalidRequest},
		{"wrong version", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, mcp.CodeInvalidRequest},
		{"missing method", `{"jsonrpc":"2.0","id":1}`, mcp.CodeInvalidRequest},
		{"scalar", `5`, mcp.CodeInvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, response := mcp.Parse([]byte(tt.body))
			if response == nil {
				t.Fatal("Parse() returned no protocol error")
			}
			if response.Error.Code != tt.code {
				t.Errorf("code = %d, want %d", response.Error.Code, tt.code)
			}
			if string(response.ID) != "null" {
				t.Errorf("id = %s, want null", response.ID)
			}
		})
	}
}

func TestParseNotificationHasNoID(t *testing.T) {
	req, response := mcp.Parse([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if response != nil {
		t.Fatalf("Parse() error = %+v", response.Error)
	}
	if !req.IsNotification() {
		t.Error("IsNotification() = false, want true")
	}
}

func TestInitializeAnnouncesToolsCapability(t *testing.T) {
	server := mcp.New("1.2.3", &fakeBackend{})
	result, errObj := server.Handle(context.Background(), mcp.Request{Method: mcp.MethodInitialize}, mcp.CallContext{})
	if errObj != nil {
		t.Fatalf("Handle() error = %+v", errObj)
	}
	body := decode(t, result)
	if body["protocolVersion"] != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", body["protocolVersion"], mcp.ProtocolVersion)
	}
	serverInfo := body["serverInfo"].(map[string]any)
	if serverInfo["name"] != "second-brain" || serverInfo["version"] != "1.2.3" {
		t.Errorf("serverInfo = %v", serverInfo)
	}
	capabilities := body["capabilities"].(map[string]any)
	if _, ok := capabilities["tools"]; !ok {
		t.Errorf("capabilities = %v, want tools", capabilities)
	}
}

func TestToolsListExposesFiveContractTools(t *testing.T) {
	server := mcp.New("test", &fakeBackend{})
	result, errObj := server.Handle(context.Background(), mcp.Request{Method: mcp.MethodToolsList}, mcp.CallContext{})
	if errObj != nil {
		t.Fatalf("Handle() error = %+v", errObj)
	}
	body := decode(t, result)
	tools := body["tools"].([]any)
	want := []string{"search", "get_record", "remember", "list_projects", "stats"}
	if len(tools) != len(want) {
		t.Fatalf("tools = %d, want %d", len(tools), len(want))
	}
	for i, entry := range tools {
		tool := entry.(map[string]any)
		if tool["name"] != want[i] {
			t.Errorf("tools[%d].name = %v, want %s", i, tool["name"], want[i])
		}
		if tool["inputSchema"] == nil {
			t.Errorf("tools[%d] has no inputSchema", i)
		}
	}
}

func TestToolScopesMatchTheContract(t *testing.T) {
	want := map[string]string{
		"search":        "mcp:read",
		"get_record":    "mcp:read",
		"list_projects": "mcp:read",
		"stats":         "mcp:read",
		"remember":      "mcp:write",
	}
	for name, scope := range want {
		tool, ok := mcp.LookupTool(name)
		if !ok {
			t.Fatalf("LookupTool(%q) not found", name)
		}
		if string(tool.Scope) != scope {
			t.Errorf("%s scope = %q, want %q", name, tool.Scope, scope)
		}
	}
	if _, ok := mcp.LookupTool("unknown"); ok {
		t.Error("LookupTool(unknown) found a tool")
	}
}

func TestToolsCallReturnsTextAndStructuredContent(t *testing.T) {
	backend := &fakeBackend{result: map[string]any{"items": []any{}, "total": 0}}
	server := mcp.New("test", backend)
	params := json.RawMessage(`{"name":"search","arguments":{"query":"fts5"}}`)
	result, errObj := server.Handle(context.Background(), mcp.Request{Method: mcp.MethodToolsCall, Params: params}, mcp.CallContext{ClientID: 7, RequestID: "req_1"})
	if errObj != nil {
		t.Fatalf("Handle() error = %+v", errObj)
	}
	body := decode(t, result)
	if body["isError"] != nil {
		t.Errorf("isError = %v, want absent/false", body["isError"])
	}
	content := body["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" {
		t.Errorf("content.type = %v", content["type"])
	}
	if content["text"] != `{"items":[],"total":0}` {
		t.Errorf("content.text = %v", content["text"])
	}
	if backend.last != "search" || backend.lastCtx.ClientID != 7 || backend.lastCtx.RequestID != "req_1" {
		t.Errorf("backend call = %q ctx=%+v", backend.last, backend.lastCtx)
	}
}

func TestToolErrorBecomesIsErrorResult(t *testing.T) {
	backend := &fakeBackend{err: &mcp.ToolError{Code: "not_found", Message: "Record 9 not found."}}
	server := mcp.New("test", backend)
	params := json.RawMessage(`{"name":"get_record","arguments":{"id":9}}`)
	result, errObj := server.Handle(context.Background(), mcp.Request{Method: mcp.MethodToolsCall, Params: params}, mcp.CallContext{RequestID: "req_9"})
	if errObj != nil {
		t.Fatalf("Handle() returned a protocol error: %+v", errObj)
	}
	body := decode(t, result)
	if body["isError"] != true {
		t.Errorf("isError = %v, want true", body["isError"])
	}
	structured := body["structuredContent"].(map[string]any)["error"].(map[string]any)
	if structured["code"] != "not_found" || structured["request_id"] != "req_9" {
		t.Errorf("structured error = %v", structured)
	}
}

func TestUnknownToolAndMethod(t *testing.T) {
	server := mcp.New("test", &fakeBackend{})
	_, errObj := server.Handle(context.Background(), mcp.Request{
		Method: mcp.MethodToolsCall,
		Params: json.RawMessage(`{"name":"nope"}`),
	}, mcp.CallContext{})
	if errObj == nil || errObj.Code != mcp.CodeInvalidParams {
		t.Fatalf("unknown tool error = %+v, want -32602", errObj)
	}

	_, errObj = server.Handle(context.Background(), mcp.Request{Method: "resources/list"}, mcp.CallContext{})
	if errObj == nil || errObj.Code != mcp.CodeMethodNotFound {
		t.Fatalf("unknown method error = %+v, want -32601", errObj)
	}
}
