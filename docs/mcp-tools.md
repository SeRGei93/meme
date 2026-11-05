# Second Brain — MCP tools v1

> Статус: контракт v1, заморожен  
> Транспорт: MCP Streamable HTTP, endpoint `POST /mcp`  
> Связанные документы: [openapi.yaml](openapi.yaml), [CONTRACTS.md](CONTRACTS.md),
> [инструкция для Jarvis](jarvis.md)

## 1. Подключение

```jsonc
{
  "mcp": {
    "servers": {
      "second-brain": {
        "type": "remote",
        "url": "https://brain.example.com/mcp",
        "oauth": false,
        "headers": {
          "Authorization": "Bearer {file:~/.config/second-brain/mcp_token}"
        },
        "codemode": false
      }
    }
  }
}
```

- Протокол: MCP `2025-06-18`. Сервер объявляет только capability `tools`.
- Транспорт: Streamable HTTP. Клиент обязан принимать оба варианта ответа: `application/json` и `text/event-stream`.
- Заголовки запроса: `Authorization: Bearer <token>`, `Content-Type: application/json`,
  `Accept: application/json, text/event-stream`.
- После `initialize` сервер может вернуть `Mcp-Session-Id`; клиент обязан повторять его в
  последующих запросах. Без сессии сервер работает stateless.
- `GET /mcp` используется только для server-initiated сообщений; v1 их не отправляет и может
  отвечать `405`.
- OAuth не используется. Токен — обычный bearer-токен из `POST /api/v1/clients`
  (`kind=agent`; устаревшие `opencode`/`jarvis` принимаются и сохраняются как `agent`).
- `source` — уникальная метка клиента, выданная вместе с токеном. OpenCode, Jarvis,
  Claude и любой другой агент подключаются одинаково.

## 2. Аутентификация, source и scope

HTTP-слой проверяет токен до JSON-RPC:

| Ситуация | Ответ |
|---|---|
| Нет/битый/отозванный токен | `401` + `ErrorEnvelope` (`unauthorized`) |
| У токена нет нужного scope | `403` + `ErrorEnvelope` (`forbidden`, `details.required_any_of`) |
| Превышен rate limit | `429` + `ErrorEnvelope` (`rate_limited`) |

`source` всегда определяется клиентом токена. В аргументах инструментов **нельзя
задать источник записи**; поле `source` есть только у `search` и работает как фильтр
по уже сохранённым записям. Любая попытка передать `source` в `remember` —
`validation_failed`. Фильтр принимает произвольную метку, включая `opencode-macbook`,
`jarvis` или `claude-prod`.

| Инструмент | Scope | Read-only | Идемпотентный |
|---|---|---|---|
| `search` | `mcp:read` | да | да |
| `get_record` | `mcp:read` | да | да |
| `list_projects` | `mcp:read` | да | да |
| `stats` | `mcp:read` | да | да |
| `remember` | `mcp:write` | нет | да (см. §5) |

Один токен может иметь несколько scope. Для вызова достаточно **любого** из scope,
перечисленных для инструмента (здесь всегда один).

## 3. Формат результата

Успешный вызов `tools/call`:

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\"items\":[],\"total\":0,\"query\":\"fts5\",\"limit\":8}"
      }
    ],
    "structuredContent": {
      "items": [],
      "total": 0,
      "query": "fts5",
      "limit": 8
    },
    "isError": false
  }
}
```

- `structuredContent` — источник истины, совпадает по схеме с REST-моделями из
  [openapi.yaml](openapi.yaml).
- `content[0].text` — тот же JSON строкой, для клиентов без structured output.
- `isError` отсутствует или `false`.
- `outputSchema` в `tools/list` — `{"type": "object"}`. Клиенты не умеют
  резолвить внешние `$ref` на `openapi.yaml` (файл не отдаётся по MCP), поэтому
  точные поля `structuredContent` заданы моделями `openapi.yaml`, а не ссылкой в
  схеме инструмента.

Ошибка выполнения инструмента (валидация, `not_found`, внутренняя ошибка) — это **не**
JSON-RPC error, а успешный JSON-RPC ответ с `isError: true`:

```json
{
  "jsonrpc": "2.0",
  "id": 8,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "not_found: Record 999 not found."
      }
    ],
    "structuredContent": {
      "error": {
        "code": "not_found",
        "message": "Record 999 not found.",
        "request_id": "req_01J"
      }
    },
    "isError": true
  }
}
```

Коды ошибок — те же, что в [CONTRACTS.md](CONTRACTS.md#3-коды-ошибок). В MCP-инструментах
встречаются: `invalid_request`, `validation_failed`, `not_found`, `conflict`,
`internal_error`, `service_unavailable`. `unauthorized`, `forbidden` и `rate_limited`
возвращаются на HTTP-уровне до JSON-RPC.

Протокольные ошибки — обычные JSON-RPC errors. На невалидный JSON-RPC сервер
отвечает HTTP `400` с этим телом; ошибки инструментов и неизвестный метод —
HTTP `200` (у метода — JSON-RPC error, у инструмента — `isError`).

| Код | Значение |
|---:|---|
| `-32700` | Parse error (тело не является JSON) |
| `-32600` | Invalid Request (не объект, неверные `jsonrpc`/`id`/`method`) |
| `-32601` | Method not found |
| `-32602` | Invalid params (неизвестный инструмент, битые аргументы) |
| `-32603` | Internal error (баг сервера, не ошибка инструмента) |

## 4. Инструменты

### 4.1 `search`

Ищет по карточкам памяти. Возвращает карточки **без** `raw_text`.

```json
{
  "name": "search",
  "description": "Search Second Brain memory. Returns ranked cards without raw text. Use get_record for the full text.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["query"],
    "properties": {
      "query": { "type": "string", "minLength": 1, "maxLength": 500 },
      "limit": { "type": "integer", "minimum": 1, "maximum": 50, "default": 8 },
      "project": { "type": "string", "minLength": 1, "maxLength": 500 },
      "source": { "type": "string", "minLength": 1, "maxLength": 100 }
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["items", "total", "query", "limit"],
    "properties": {
      "items": {
        "type": "array",
        "items": { "$ref": "openapi.yaml#/components/schemas/SearchHit" }
      },
      "total": { "type": "integer", "minimum": 0 },
      "query": { "type": "string" },
      "limit": { "type": "integer", "minimum": 1 }
    }
  },
  "annotations": { "readOnlyHint": true, "idempotentHint": true }
}
```

Отображение аргументов: `query` → `q`, `project` → `project_key`,
`source` — фильтр, не источник записи.
`total` — число совпадений до применения `limit`.

Пример вызова:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "search",
    "arguments": { "query": "миграции sqlite", "limit": 5 }
  }
}
```

### 4.2 `get_record`

Возвращает полную запись, включая `raw_text`.

```json
{
  "name": "get_record",
  "description": "Return one record with its card fields and full raw text.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["id"],
    "properties": {
      "id": { "type": "integer", "format": "int64", "minimum": 1 }
    }
  },
  "outputSchema": { "$ref": "openapi.yaml#/components/schemas/Record" },
  "annotations": { "readOnlyHint": true, "idempotentHint": true }
}
```

Неизвестный `id` → `isError: true`, `code=not_found`.

### 4.3 `remember`

Сохраняет факт, решение или заметку. Source берётся из токена.

```json
{
  "name": "remember",
  "description": "Store a fact, decision or note in Second Brain. Use a stable external_id when retrying.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "required": ["text"],
    "properties": {
      "text": { "type": "string", "minLength": 1, "maxLength": 2000000 },
      "title": { "type": "string", "minLength": 1, "maxLength": 500 },
      "summary": { "type": "string", "maxLength": 5000 },
      "project": { "type": "string", "minLength": 1, "maxLength": 500, "default": "jarvis" },
      "tags": {
        "type": "array",
        "maxItems": 100,
        "items": { "type": "string", "minLength": 1, "maxLength": 200 }
      },
      "external_id": { "type": "string", "minLength": 1, "maxLength": 200 },
      "occurred_at": { "type": "string", "format": "date-time" }
    }
  },
  "outputSchema": {
    "type": "object",
    "required": ["record_id", "job_id", "status", "replayed"],
    "properties": {
      "record_id": { "type": "integer", "format": "int64", "minimum": 1 },
      "job_id": { "type": ["integer", "null"], "format": "int64" },
      "status": { "type": "string", "enum": ["indexed", "queued"] },
      "replayed": { "type": "boolean" }
    }
  },
  "annotations": { "readOnlyHint": false, "destructiveHint": false, "idempotentHint": true }
}
```

Отображение аргументов: `text` → `raw_text`, `project` → `project_key`, `tags` → `keywords`,
`title`, `summary`, `external_id`, `occurred_at` передаются как есть.

- Если `title` и `summary` не переданы, сервер сам сгенерирует карточку: `status=queued`,
  `job_id` заполнен.
- Если передана полная карточка (`title` и `summary`), LLM не вызывается:
  `status=indexed`, `job_id=null`.
- `replayed=true` означает, что запись уже существовала и повтор не создал дубль.
- `external_id` настоятельно рекомендуется: по нему работает идемпотентность. Без него
  сервер выводит стабильный id из содержимого, поэтому два одинаковых `remember` подряд
  схлопнутся в одну запись.

Пример:

```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "tools/call",
  "params": {
    "name": "remember",
    "arguments": {
      "text": "Решение: используем SQLite FTS5 вместо pgvector",
      "project": "second-brain",
      "tags": ["decision", "sqlite"],
      "external_id": "decision-fts5"
    }
  }
}
```

### 4.4 `list_projects`

```json
{
  "name": "list_projects",
  "description": "List projects with record counts, most recently active first.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false,
    "properties": {
      "limit": { "type": "integer", "minimum": 1, "maximum": 500, "default": 100 }
    }
  },
  "outputSchema": { "$ref": "openapi.yaml#/components/schemas/ProjectList" },
  "annotations": { "readOnlyHint": true, "idempotentHint": true }
}
```

### 4.5 `stats`

```json
{
  "name": "stats",
  "description": "Return record, project, client and queue counts plus database state.",
  "inputSchema": {
    "type": "object",
    "additionalProperties": false
  },
  "outputSchema": { "$ref": "openapi.yaml#/components/schemas/Stats" },
  "annotations": { "readOnlyHint": true, "idempotentHint": true }
}
```

## 5. Идемпотентность `remember`

Ключ идемпотентности — `(client, source, external_id)`.

1. `external_id` передан → повтор с тем же `external_id` и тем же содержимым возвращает
   существующую запись и `replayed=true`; другое содержимое с тем же `external_id` —
   `isError: true`, `code=conflict`.
2. `external_id` не передан → сервер считает SHA-256 от нормализованного содержимого и
   использует его как `external_id`. Повтор идентичного вызова возвращает существующую
   запись; изменившийся текст создаёт новую.

Ретраи без `external_id` безопасны, но два разных факта с одинаковым текстом неразличимы.
Если это важно — всегда передавайте `external_id`.

## 6. Retry

- `mcp:read` инструменты безопасно повторять всегда.
- `remember` повторяйте только при наличии `external_id` или идентичного текста.
- Повторяйте при network error, `429`, `500`, `502`, `503`, `504`; не повторяйте прочие
  `4xx`.
- Уважайте `Retry-After`.
- Backoff: экспоненциальный с jitter, база 1 с, множитель 2, максимум 60 с, не более 3
  попыток для интерактивного агента.
- Полный `ErrorEnvelope` с `request_id` — в теле HTTP-ответа; сохраняйте его в логах.

## 7. Лимиты

| Лимит | Значение |
|---|---|
| Размер JSON-RPC запроса | 10 MiB |
| `text` в `remember` | 2 000 000 символов |
| `tags` | 100 |
| `limit` в `search` | 50 |
| `limit` в `list_projects` | 500 |
| Rate limit (по умолчанию) | 120 чтений/мин и 30 записей/мин на токен |

При превышении — `429` c `Retry-After`; для слишком большого тела — HTTP `413`.
