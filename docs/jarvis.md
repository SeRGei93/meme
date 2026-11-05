# Jarvis → Second Brain

Jarvis живёт на отдельном сервере и подключается к памяти как обычный удалённый
MCP-клиент. Отдельного плагина или адаптера нет: Jarvis хранит свой bearer-токен
в секрете/переменной окружения и ходит в `POST /mcp`.

- Endpoint: `https://meme.aimerce.by/mcp`
- Транспорт: MCP Streamable HTTP, протокол `2025-06-18`
- Основной путь: MCP `remember` / `search` / `get_record` / `list_projects` / `stats`
- Резервный путь: HTTP `POST /api/v1/memories`, `GET /api/v1/search`

Решение по клиентам и источникам: [ADR-11](adr/0011-generic-clients-and-sources.md).

## 1. Выпуск токена

Токен выпускает только admin. Клиент `jarvis` получает уникальный
`source=jarvis` и scope `mcp:read`, `mcp:write`, `ingest` (`ingest` нужен только
для HTTP fallback).

```bash
export BRAIN_URL=https://meme.aimerce.by
export BRAIN_ADMIN_TOKEN=...        # bootstrap admin token, не хранится в БД

brain client create \
  --url "$BRAIN_URL" \
  --name jarvis \
  --source jarvis \
  --scopes mcp:read,mcp:write,ingest
```

Эквивалент через API:

```bash
curl -fsS -X POST "$BRAIN_URL/api/v1/clients" \
  -H "Authorization: Bearer $BRAIN_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "jarvis",
    "kind": "agent",
    "source": "jarvis",
    "scopes": ["mcp:read", "mcp:write", "ingest"]
  }'
```

Ответ содержит `client` и `token`. Токен показывается **один раз**; положите его
в секрет Jarvis, например `BRAIN_TOKEN`, и не пишите в логи, git или Keychain
macOS-приложения.

Если `source` не указан, сервер возьмёт slug имени (`Jarvis` → `jarvis`).
`source` неизменяем и уникален среди активных клиентов.

## 2. Конфигурация Jarvis

Подставьте URL и секрет в конфиг MCP-клиента Jarvis. Формат зависит от его
фреймворка; важны только transport, URL и заголовок авторизации. Ниже — пример
в формате OpenCode; Jarvis подставляет эквивалент своего MCP-клиента.

```json
{
  "mcp": {
    "servers": {
      "second-brain": {
        "type": "remote",
        "url": "https://meme.aimerce.by/mcp",
        "oauth": false,
        "headers": {
          "Authorization": "Bearer ${BRAIN_TOKEN}",
          "Accept": "application/json, text/event-stream"
        },
        "codemode": false
      }
    }
  }
}
```

Заголовки каждого запроса:

```http
Authorization: Bearer <jarvis token>
Content-Type: application/json
Accept: application/json, text/event-stream
```

Если сервер вернул `Mcp-Session-Id`, повторяйте его в последующих запросах.
Сервер работает stateless, поэтому сессия не обязательна.

## 3. MCP: initialize и tools/list

```bash
JARVIS_TOKEN=...   # из секрета Jarvis

curl -fsS "$BRAIN_URL/mcp" \
  -H "Authorization: Bearer $JARVIS_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-06-18",
      "capabilities": {},
      "clientInfo": {"name": "jarvis", "version": "1.0"}
    }
  }'
```

`tools/list` возвращает пять инструментов: `remember`, `search`, `get_record`,
`list_projects`, `stats`.

## 4. MCP: remember

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "remember",
    "arguments": {
      "text": "Решение: используем SQLite FTS5 вместо pgvector.",
      "title": "Выбор FTS5",
      "summary": "v1 хранит память в SQLite с FTS5.",
      "project": "second-brain",
      "tags": ["decision", "sqlite"],
      "external_id": "jarvis-2026-11-05-fts5"
    }
  }
}
```

- `external_id` — стабильный ключ идемпотентности. Для повторной попытки
  используйте **тот же** `external_id`.
- `source` в аргументах **запрещён**: источник всегда берётся из токена.
  Попытка передать `source` вернёт `validation_failed`.
- Если переданы `title` и `summary`, карточка сохраняется без LLM
  (`status: "indexed"`). Иначе запись ставится в очередь (`status: "queued"`).

Успешный ответ:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "structuredContent": {
      "record_id": 42,
      "job_id": null,
      "status": "indexed",
      "replayed": false
    }
  }
}
```

Повтор с тем же `external_id` и тем же содержимым возвращает `replayed: true` и
тот же `record_id`. Другое содержимое с тем же `external_id` — `conflict`.

## 5. MCP: search

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "search",
    "arguments": {
      "query": "FTS5",
      "limit": 5
    }
  }
}
```

`source` — необязательный фильтр по уже сохранённым записям. Jarvis может искать
по всей памяти, а не только по своим записям:

```json
{"query": "FTS5", "limit": 5, "source": "jarvis"}
```

Результаты — карточки без `raw_text`; полный текст забирается через
`get_record` с `{"id": 42}`.

## 6. HTTP fallback

Если MCP недоступен, тот же токен работает через REST:

```bash
curl -fsS -X POST "$BRAIN_URL/api/v1/memories" \
  -H "Authorization: Bearer $JARVIS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "external_id": "jarvis-2026-11-05-fts5",
    "project_key": "second-brain",
    "title": "Выбор FTS5",
    "summary": "v1 хранит память в SQLite с FTS5.",
    "raw_text": "Решение: используем SQLite FTS5 вместо pgvector."
  }'

curl -fsS "$BRAIN_URL/api/v1/search?q=FTS5&source=jarvis" \
  -H "Authorization: Bearer $JARVIS_TOKEN"
```

`Idempotency-Key` необязателен: без него ключ выводится из `external_id`/тела.
Повтор с тем же содержимым возвращает `200` и `replayed: true`.

## 7. Ошибки и ретраи

| Ответ | Что делать |
|---|---|
| `401 unauthorized` | Токен отсутствует, битый или отозван. Выпустить новый, обновить секрет. |
| `403 forbidden` | У клиента нет нужного scope или source. Проверить `mcp:read`/`mcp:write`. |
| `409 conflict` | Тот же `external_id` с другим содержимым. Новый `external_id` или считать запись новой. |
| `422 validation_failed` (REST) | Исправить аргументы; `source` в `remember` запрещён. В MCP это `200` + `isError`. |
| `429 rate_limited` | Ждать `Retry-After`, повторить. |
| `5xx`, network, timeout | Повторить **то же тело** с тем же `external_id`, full jitter, до 3 попыток. |

MCP-ошибки инструментов приходят с HTTP `200` и `result.isError: true`;
код лежит в `result.structuredContent.error.code`. `401`, `403` и `429`
приходят HTTP-ответом до JSON-RPC.

Рекомендуемая политика Jarvis: 3 попытки, `base=1s`, `cap=60s`,
`delay = random(0, min(cap, base * 2^attempt))`, уважать `Retry-After`.
Не повторять `400`, `401`, `403`, `404`, `409`, `413`, `415`, `422`.

Идемпотентность считается в пределах `(client_id, source)`: после выпуска нового
клиента тот же `external_id` создаст новую запись. Если нужна полная
непрерывность, сохраняйте тот же source (см. §8) и передавайте те же тела.

`429 rate_limited` появится после включения rate limiting на сервере; клиенту
безопасно обрабатывать его уже сейчас.

## 8. Ротация и отзыв

Отзыв действует немедленно: `DELETE /api/v1/clients/{id}` с admin-токеном.
После него все запросы с отозванным токеном получают `401`.

```bash
curl -fsS -X DELETE "$BRAIN_URL/api/v1/clients/<id>" \
  -H "Authorization: Bearer $BRAIN_ADMIN_TOKEN"
```

Порядок ротации:

1. Если нужен новый source — выпустить нового клиента **с новым именем**
   (имена активных клиентов тоже уникальны) и обновить секрет Jarvis.
2. Если нужен тот же `source=jarvis`, сначала отозвать старого клиента, затем
   выпустить нового с этим source (метка освобождается только после отзыва).
   Имя можно переиспользовать: оно тоже освобождается после отзыва.
3. Проверить `remember` и `search` новым токеном, затем удалить старый секрет.

Не храните admin-токен в Jarvis: ему нужен только клиентский токен.

## 9. Проверка

Локальный end-to-end (реальный HTTP-слой, без внешнего Jarvis):

```bash
cd server && go test -run 'TestJarvis' ./internal/httpapi
```

Проверка на живом сервере (нужны `curl` и `jq`):

```bash
JARVIS_TOKEN_FILE=~/.config/second-brain/jarvis_token \
  BRAIN_URL=https://meme.aimerce.by tools/jarvis-smoke.sh
```

`JARVIS_TOKEN` тоже поддерживается, но файл не оставляет токен в истории shell.
`JARVIS_SOURCE` переопределяет source для поиска (по умолчанию `jarvis`).
Скрипт делает `initialize`, `tools/list`, `remember` с уникальным `external_id`
и проверяет, что `search` вернул именно этот `record_id`; токен он не отзывает.
После запуска запись видна в приложении и находится поиском по source `jarvis`.
