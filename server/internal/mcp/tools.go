package mcp

// ToolInfo is the tools/list entry from docs/mcp-tools.md.
type ToolInfo struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

var readOnly = map[string]any{"readOnlyHint": true, "idempotentHint": true}

// MCP clients cannot resolve external $refs (openapi.yaml is not served over
// MCP), and a loose object schema still matches every structuredContent this
// server returns. The concrete field shapes live in docs/openapi.yaml.
var objectSchema = map[string]any{"type": "object"}

var searchInfo = ToolInfo{
	Name:        "search",
	Description: "Search Second Brain memory. Returns ranked cards without raw text. Use get_record for the full text.",
	InputSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"query"},
		"properties": map[string]any{
			"query":   map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 8},
			"project": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"source":  map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
		},
	},
	OutputSchema: objectSchema,
	Annotations:  readOnly,
}

var getRecordInfo = ToolInfo{
	Name:        "get_record",
	Description: "Return one record with its card fields and full raw text.",
	InputSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"id"},
		"properties": map[string]any{
			"id": map[string]any{"type": "integer", "format": "int64", "minimum": 1},
		},
	},
	OutputSchema: objectSchema,
	Annotations:  readOnly,
}

var rememberInfo = ToolInfo{
	Name:        "remember",
	Description: "Store a fact, decision or note in Second Brain. Use a stable external_id when retrying.",
	InputSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"text"},
		"properties": map[string]any{
			"text":        map[string]any{"type": "string", "minLength": 1, "maxLength": 2_000_000},
			"title":       map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"summary":     map[string]any{"type": "string", "maxLength": 5_000},
			"project":     map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "default": "jarvis"},
			"tags":        map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}},
			"external_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"occurred_at": map[string]any{"type": "string", "format": "date-time"},
		},
	},
	OutputSchema: objectSchema,
	Annotations:  map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
}

var listProjectsInfo = ToolInfo{
	Name:        "list_projects",
	Description: "List projects with record counts, most recently active first.",
	InputSchema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500, "default": 100},
		},
	},
	OutputSchema: objectSchema,
	Annotations:  readOnly,
}

var statsInfo = ToolInfo{
	Name:         "stats",
	Description:  "Return record, project, client and queue counts plus database state.",
	InputSchema:  map[string]any{"type": "object", "additionalProperties": false},
	OutputSchema: objectSchema,
	Annotations:  readOnly,
}
