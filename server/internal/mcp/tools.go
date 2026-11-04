package mcp

// ToolInfo is the tools/list entry from docs/mcp-tools.md.
type ToolInfo struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

func toolInfo(info ToolInfo) ToolInfo { return info }

var readOnly = map[string]any{"readOnlyHint": true, "idempotentHint": true}

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
			"source":  map[string]any{"type": "string", "enum": []any{"opencode", "jarvis"}},
		},
	},
	OutputSchema: map[string]any{
		"type":     "object",
		"required": []any{"items", "total", "query", "limit"},
		"properties": map[string]any{
			"items": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "openapi.yaml#/components/schemas/SearchHit"},
			},
			"total": map[string]any{"type": "integer", "minimum": 0},
			"query": map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer", "minimum": 1},
		},
	},
	Annotations: readOnly,
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
	OutputSchema: map[string]any{"$ref": "openapi.yaml#/components/schemas/Record"},
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
	OutputSchema: map[string]any{
		"type":     "object",
		"required": []any{"record_id", "job_id", "status", "replayed"},
		"properties": map[string]any{
			"record_id": map[string]any{"type": "integer", "format": "int64", "minimum": 1},
			"job_id":    map[string]any{"type": []any{"integer", "null"}, "format": "int64"},
			"status":    map[string]any{"type": "string", "enum": []any{"indexed", "queued"}},
			"replayed":  map[string]any{"type": "boolean"},
		},
	},
	Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
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
	OutputSchema: map[string]any{"$ref": "openapi.yaml#/components/schemas/ProjectList"},
	Annotations:  readOnly,
}

var statsInfo = ToolInfo{
	Name:         "stats",
	Description:  "Return record, project, client and queue counts plus database state.",
	InputSchema:  map[string]any{"type": "object", "additionalProperties": false},
	OutputSchema: map[string]any{"$ref": "openapi.yaml#/components/schemas/Stats"},
	Annotations:  readOnly,
}
