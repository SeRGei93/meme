# brain

Go-сервер Second Brain: REST API, SQLite + FTS5, auth. Все команды выполняются
из этой директории (`server/`).

## Переменные

| Переменная | Обязательна | По умолчанию | Назначение |
|---|:---:|---|---|
| `BRAIN_ADMIN_TOKEN` | да | — | Bootstrap admin-токен; в БД не хранится |
| `BRAIN_ADDR` | нет | `:8080` | Адрес HTTP-сервера |
| `BRAIN_DB` | нет | `brain.db` | Путь к SQLite-файлу |
| `BRAIN_LLM_BASE_URL` | нет | — | OpenAI-совместимый endpoint (`…/v1`); без него генерация карточек выключена |
| `BRAIN_LLM_API_KEY` | нет | — | Bearer-ключ LLM; можно не задавать для локального endpoint |
| `BRAIN_LLM_MODEL` | нет | — | Модель для генерации карточек |
| `BRAIN_LLM_MAX_INPUT_CHARS` | нет | `24000` | Лимит символов текста сессии в промпте |

## Локальный запуск

```bash
export BRAIN_ADMIN_TOKEN="$(openssl rand -hex 32)"
BRAIN_DB=./brain.db go run ./cmd/brain serve
```

Проверка и выпуск первого клиентского токена:

```bash
curl -s http://127.0.0.1:8080/health

curl -s -X POST http://127.0.0.1:8080/api/v1/clients \
  -H "Authorization: Bearer $BRAIN_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"opencode-macbook","kind":"opencode","scopes":["ingest"]}'
```

`brain healthcheck` проверяет `/health` на `BRAIN_ADDR` и завершается с кодом
`1` при ошибке. Этот же режим используется Docker healthcheck.

## Тесты

```bash
go test ./...
go vet ./...
```

## Docker

Контекст сборки — эта директория, не корень репозитория:

```bash
docker build -t brain .
docker run --rm -p 8080:8080 \
  -e BRAIN_ADMIN_TOKEN="$(openssl rand -hex 32)" \
  -v brain-data:/data \
  brain
```

Образ multi-stage, работает от non-root (`65532:65532`), БД лежит в `/data`.
