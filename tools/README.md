# Tools: contracts, mock API, validator

Вспомогательные инструменты для контрактов v1. Зависимости ставятся локально:

```bash
cd tools
npm install
```

## Команды

| Команда | Что делает |
|---|---|
| `npm test` | Гоняет тесты схем, OpenAPI и mock API |
| `npm run validate:openapi` | Redocly lint для `docs/openapi.yaml` |
| `npm run validate:spool -- <files>` | Валидирует spool-бандлы по `docs/spool.schema.json` |
| `npm run mock` | Поднимает in-memory mock API на `http://127.0.0.1:8080` |

### Валидатор бандлов

```bash
node validate-spool.mjs ../tools/fixtures/spool-bundle.example.json
cat bundle.json | node validate-spool.mjs --stdin
```

Коды выхода: `0` — всё валидно, `1` — есть невалидный бандл, `2` — ошибка аргументов.
Ошибки печатаются в stderr с путём до поля, например:

```text
invalid: /tmp/broken.json
  /title must NOT have fewer than 1 characters
```

### Mock API

```bash
npm run mock
# PORT=9090 BRAIN_ADMIN_TOKEN=secret npm run mock
```

- Реализованы все эндпоинты `docs/openapi.yaml`, включая `/health`.
- Токены и scope проверяются по-настоящему; bootstrap admin-токен — `BRAIN_ADMIN_TOKEN`
  (по умолчанию `dev-admin-token`).
- `POST /api/v1/ingest` валидируется той же JSON-схемой, что и spool-бандлы.
- Поддерживаются `Idempotency-Key`, cursor-пагинация jobs и отзыв клиентов.
- Данные хранятся в памяти и исчезают при рестарте.
- Карточки не генерируются: job'ы остаются в `queued`.
- Поиск — упрощённый substring-match, не BM25.
- Rate limit не применяется.

## Структура

```text
tools/
  lib/spool-schema.mjs      # загрузка и компиляция JSON-схемы
  validate-spool.mjs        # CLI-валидатор
  mock-api.mjs              # mock REST API
  fixtures/                 # примеры бандлов
  test/                     # контрактные тесты
  redocly.yaml              # конфиг линтера
```
