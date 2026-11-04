# Second Brain — контракты v1

> Статус: заморожено для v1  
> Дата: 2026-11-03  
> Связанные документы: [ARCHITECTURE.md](ARCHITECTURE.md), [ROADMAP.md](ROADMAP.md),
> [openapi.yaml](openapi.yaml), [spool.schema.json](spool.schema.json),
> [mcp-tools.md](mcp-tools.md)

Этот документ фиксирует правила, общие для сервера, OpenCode plugin, macOS-приложения и
Jarvis: токены, scope, ошибки, ретраи, идемпотентность и лимиты. Эндпоинты и схемы описаны
в `openapi.yaml`, spool-бандл — в `spool.schema.json`, MCP-инструменты — в `mcp-tools.md`.

## 1. Артефакты контракта

| Артефакт | Путь | Для кого |
|---|---|---|
| REST API v1 | `docs/openapi.yaml` | сервер, приложение, Jarvis |
| Spool-бандл v1 | `docs/spool.schema.json` | plugin, приложение |
| Пример бандла | `tools/fixtures/spool-bundle.example.json` | plugin, тесты |
| MCP tools v1 | `docs/mcp-tools.md` | сервер, OpenCode, Jarvis |
| Правила (этот файл) | `docs/CONTRACTS.md` | все |
| Валидатор бандлов | `tools/validate-spool.mjs` | plugin, CI |
| Mock API | `tools/mock-api.mjs` | приложение, CI |
| Контрактные тесты | `tools/test/*.test.mjs` | все |

## 2. Версионирование

- Путь `/api/v1` и `schema_version: 1` заморожены.
- **Разрешено без смены версии:** новые необязательные поля в ответах, новые эндпоинты,
  новые инструменты MCP, новые коды ошибок, новые необязательные query-параметры.
- **Требует `/api/v2`:** удаление или переименование поля, смена типа, смена смысла кода
  ошибки, изменение правил идемпотентности, ужесточение обязательности полей запроса.
- Клиенты обязаны игнорировать неизвестные поля в ответах. Сервер обязан отклонять
  неизвестные поля в запросах (`additionalProperties: false`), чтобы опечатки не терялись.
- `messages[].tool_calls` — аддитивное поле v1, добавленное по M3 (нормализатор tool calls).
  Бандл из архитектуры без этого поля остаётся валидным. `schema_version` необязателен:
  при отсутствии считается равным `1`.
- Любое изменение контракта обновляет все артефакты и тесты в одном PR.

## 3. Коды ошибок

Все ошибки REST используют один конверт:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "messages[0].role must be one of user, assistant, system, tool",
    "details": { "field": "messages[0].role" },
    "request_id": "req_01J"
  }
}
```

| Код | HTTP | Retryable | Когда возникает |
|---|---:|:---:|---|
| `invalid_request` | 400 | нет | битый JSON, неверный query/path-параметр, пустое тело |
| `validation_failed` | 422 | нет | JSON валиден, но нарушает схему или бизнес-правило |
| `unauthorized` | 401 | нет | токен отсутствует, битый или отозван |
| `forbidden` | 403 | нет | у токена нет нужного scope или kind не может выполнять операцию |
| `not_found` | 404 | нет | запись, клиент или маршрут не найдены |
| `method_not_allowed` | 405 | нет | метод не поддерживается для маршрута |
| `conflict` | 409 | нет | дубликат имени клиента, `external_id` с другим содержимым |
| `idempotency_key_reused` | 409 | нет | тот же `Idempotency-Key`, но другое тело |
| `payload_too_large` | 413 | нет | тело больше 10 MiB |
| `unsupported_media_type` | 415 | нет | `Content-Type` не `application/json` |
| `rate_limited` | 429 | да | превышен лимит; смотрите `Retry-After` |
| `internal_error` | 500 | да | непредвиденная ошибка сервера |
| `service_unavailable` | 503 | да | старт/остановка, блокировка БД, временная недоступность |

Правила:

- `code` стабилен; `message` — для человека, парсить нельзя.
- `details` необязателен и зависит от кода (`field`, `required_any_of`, `required_kind`,
  `retry_after_seconds`, `allow`).
- `request_id` дублируется в заголовке `X-Request-Id` и в логах сервера. Прикладывайте его
  к баг-репорту.
- MCP использует те же коды внутри `structuredContent.error` при `isError: true`.
  `unauthorized`, `forbidden` и `rate_limited` в MCP приходят HTTP-ответом до JSON-RPC.

## 4. Токены и scope

### 4.1 Формат и хранение

- Opaque bearer-токен: `sb_<kind>_<43 символа base64url>` (32 случайных байта).
- Сервер хранит только SHA-256 хеш. Plaintext возвращается один раз в
  `POST /api/v1/clients` и больше нигде.
- Bootstrap admin-токен задаётся `BRAIN_ADMIN_TOKEN` и не хранится в БД. Он обладает всеми
  scope и не появляется в `GET /api/v1/clients`.
- Токены передаются только в `Authorization: Bearer ...`. Query-параметры, логи и тела
  ошибок не должны содержать токен.
- TLS обязателен для всех внешних соединений.
- Срок жизни в v1 не ограничен. Ротация = создать нового клиента, обновить конфиг,
  отозвать старого. Отзыв действует немедленно.
- Утечка: отозвать клиента через `DELETE /api/v1/clients/{id}`, выпустить нового.
  Bootstrap-токен меняется редеплоем с новым `BRAIN_ADMIN_TOKEN`.

### 4.2 Kind → source

`source` никогда не принимается из тела запроса. Он выводится из `kind` клиента:

| `kind` | `source` | `POST /ingest` | `POST /memories` | MCP |
|---|---|---|---|---|
| `opencode` | `opencode` | да | да (команда `/remember`) | да |
| `jarvis` | `jarvis` | нет, `403` | да | да |
| `ui` | `null` | нет | нет | нет |
| `admin` | `null` | нет | нет | нет |

Попытка передать `source` в теле `POST /ingest` или `POST /memories` — `422
validation_failed`, потому что поле не входит в схему запроса.

### 4.3 Scope

Scope аддитивны. Для эндпоинта с несколькими scope достаточно **любого** одного —
это указано в OpenAPI через `x-required-scopes`.

| Операция | `ingest` | `mcp:read` | `mcp:write` | `ui` | `admin` |
|---|:---:|:---:|:---:|:---:|:---:|
| `POST /api/v1/ingest` | ✓ | | | | |
| `POST /api/v1/memories` | ✓ | | | | |
| `GET /api/v1/search` | | ✓ | | ✓ | |
| `GET /api/v1/records/{id}` | | ✓ | | ✓ | |
| `GET /api/v1/projects` | | ✓ | | ✓ | |
| `GET /api/v1/graph` | | | | ✓ | |
| `GET /api/v1/stats` | | ✓ | | ✓ | |
| `GET /api/v1/clients` | | | | | ✓ |
| `POST /api/v1/clients` | | | | | ✓ |
| `DELETE /api/v1/clients/{id}` | | | | | ✓ |
| `GET /api/v1/jobs` | | | | | ✓ |
| MCP `search` | | ✓ | | | |
| MCP `get_record` | | ✓ | | | |
| MCP `list_projects` | | ✓ | | | |
| MCP `stats` | | ✓ | | | |
| MCP `remember` | | | ✓ | | |

Команда `/remember` пишет через HTTP `POST /api/v1/memories`, поэтому одному
клиенту `opencode` нужны и MCP-scope, и `ingest`; onboarding выдаёт такой набор.

Рекомендуемые наборы:

| Клиент | Kind | Scope |
|---|---|---|
| macOS app (upload) | `opencode` | `ingest` |
| macOS app (UI) | `ui` | `ui`, `admin` |
| OpenCode plugin MCP + `/remember` | `opencode` | `ingest`, `mcp:read`, `mcp:write` |
| Jarvis | `jarvis` | `ingest`, `mcp:read`, `mcp:write` |

## 5. Retry-политика

### 5.1 Что повторять

| Результат | Повторять | Примечание |
|---|:---:|---|
| network error, timeout | да | только с `Idempotency-Key` для записи |
| `408`, `425`, `429` | да | `Retry-After` обязателен для `429` |
| `500`, `502`, `503`, `504` | да | с `Idempotency-Key` для записи |
| `400`, `401`, `403`, `404`, `409`, `413`, `415`, `422` | нет | исправить запрос/токен |
| `2xx` | нет | успех |

### 5.2 Backoff

- Экспоненциальный с full jitter: `delay = random(0, min(cap, base * 2^attempt))`.
- `base = 1s`, `cap = 60s`.
- Если есть `Retry-After` — ждать не меньше указанного числа секунд.
- Максимум попыток:

| Клиент | Попыток | Примечание |
|---|---:|---|
| macOS app upload | 5 | бандл остаётся в spool до успеха |
| OpenCode MCP | 3 | интерактивно, не блокировать агента дольше |
| Jarvis HTTP/MCP | 3 | при `remember` обязателен стабильный `external_id` |

### 5.3 Поведение приложения при ошибках

- `202`/`200` — удалить бандл из spool.
- `4xx`, кроме `401`/`403`/`408`/`429` — переместить в `spool/failed/`, показать в UI.
- `401`/`403` — остановить очередь, показать «нужен новый токен», не удалять бандлы.
- `5xx`/network/`429` — оставить бандл, повторить с backoff.
- Повторная отправка обязана использовать `Idempotency-Key = bundle_id`.

## 6. Идемпотентность

### 6.1 `Idempotency-Key`

- Заголовок `Idempotency-Key`, 1–128 печатных ASCII-символов.
- Область действия: `(клиент, операция, ключ)`, хранится 24 часа.
- Если заголовок не передан:
  - `POST /api/v1/ingest` — ключ = `bundle_id`;
  - `POST /api/v1/memories` — ключ = SHA-256 содержимого.
- MCP `remember` не использует HTTP-заголовок: его ключ — `(client, source, external_id)`,
  где `external_id` задан явно или выведен из SHA-256 содержимого (см. §6.4).
- Тот же ключ + то же тело → `200` и исходный ответ с `replayed: true`.
- Тот же ключ + другое тело → `409 idempotency_key_reused`; исходная операция не
  выполняется.
- Повтор не создаёт новых записей и не ставит новые job'ы.

### 6.2 Естественная идемпотентность ingest

Ключ записи: `(client_id, source, external_id)`.

- Первый ingest создаёт запись и, если `final=true`, job генерации карточки.
- Повтор с тем же `content_hash` не меняет запись и не создаёт новый job; сервер отвечает
  `200` с `replayed=true` и возвращает существующий `record_id`/`job_id`.
- Изменение содержимого увеличивает `revision`, обновляет `raw_text` и ставит новую
  карточку в очередь, если `final=true`.
- `final=false` обновляет только `raw_text`; карточка не генерируется.
- Один и тот же `external_id` от **разных** клиентов — разные записи.

### 6.3 Memories

Ключ записи: `(client_id, source, external_id)`.

- `external_id` не передан → сервер выводит `auto_<hash>`; повтор идентичного тела
  возвращает ту же запись.
- Тот же `external_id` + другое содержимое → `409 conflict`. Вызывающий должен либо
  использовать новый `external_id`, либо считать это новой записью.
- Полная карточка (`title` + `summary`) сохраняется без LLM: `status=indexed`.
- Только текст → `status=queued`, `job_id` заполнен.

### 6.4 MCP `remember`

Правила совпадают с memories. HTTP-заголовка `Idempotency-Key` в MCP нет, поэтому
`idempotency_key_reused` не возникает: тот же `external_id` с другим содержимым всегда
даёт `conflict`. Подробности — в [mcp-tools.md](mcp-tools.md#5-идемпотентность-remember).

## 7. Лимиты

| Лимит | Значение | Ответ при превышении |
|---|---:|---|
| Тело HTTP-запроса | 10 MiB | `413 payload_too_large` |
| `messages` в бандле | 10 000 | `422 validation_failed` |
| `text` сообщения | 200 000 символов | `422 validation_failed` |
| `title` | 500 символов | `422 validation_failed` |
| `raw_text` памяти | 2 000 000 символов | `422 validation_failed` |
| `keywords` | 100 | `422 validation_failed` |
| `GET /search?limit` | 50 | `400 invalid_request` |
| `GET /graph?limit` | 500 | `400 invalid_request` |
| Rate limit, чтение | 120/мин на токен | `429 rate_limited` |
| Rate limit, запись | 30/мин на токен | `429 rate_limited` |

Rate limit — значение по умолчанию; сервер может быть строже. Клиенты обязаны корректно
обрабатывать `429` и `Retry-After` независимо от фактических настроек.

## 8. Как пользоваться контрактами

### 8.1 Валидатор бандлов для plugin

```bash
cd tools
npm install
npm run validate:spool -- fixtures/spool-bundle.example.json
# или
cat bundle.json | node validate-spool.mjs --stdin
```

Валидатор использует `docs/spool.schema.json` и завершается с кодом `1`, если бандл
невалиден. Его можно вызывать из тестов плагина и из CI.

### 8.2 Mock API для приложения

```bash
cd tools
npm install
npm run mock
# Second Brain mock API listening on http://127.0.0.1:8080
# Admin token: dev-admin-token
```

Mock API реализует все эндпоинты `openapi.yaml`, проверяет токены и scope, валидирует
ingest по `spool.schema.json`, поддерживает идемпотентность и хранит данные в памяти.
Он **не** генерирует карточки (job'ы остаются в `queued`), не делает BM25 и не применяет
rate limit — этого достаточно для UI и интеграционных тестов приложения.

Переменные: `PORT` (по умолчанию `8080`), `HOST` (`127.0.0.1`),
`BRAIN_ADMIN_TOKEN` (`dev-admin-token`).

### 8.3 Проверка контрактов

```bash
cd tools
npm test                    # схемы, OpenAPI, mock API
npm run validate:openapi    # Redocly lint
npm run validate:spool -- fixtures/spool-bundle.example.json
```

## 9. Чек-лист готовности контрактов

- [x] OpenAPI валиден и покрывает `/health`, ingest, memories, search, records, projects,
      graph, stats, clients (GET/POST/DELETE), jobs.
- [x] Spool-схема валидна и описывает все поля бандла из архитектуры.
- [x] MCP-контракт покрывает `search`, `get_record`, `remember`, `list_projects`, `stats`,
      их scope и формат ошибок.
- [x] Зафиксированы коды ошибок, retry-политика и правила идемпотентности.
- [x] Зафиксированы scope `ingest`, `mcp:read`, `mcp:write`, `ui`, `admin`; `source`
      определяется токеном.
- [x] По контрактам можно поднять mock API и валидировать бандлы.
