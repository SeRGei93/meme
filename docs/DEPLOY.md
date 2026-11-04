# Деплой brain в Dokploy

Один контейнер, одна реплика, SQLite на именованном volume. Конфигурация стека —
[`docker-compose.yml`](../docker-compose.yml) в корне репозитория. Секреты задаются
только через Environment в Dokploy, в git их нет.

## 1. Подготовить секреты

```bash
openssl rand -hex 32   # BRAIN_ADMIN_TOKEN
```

`BRAIN_ADMIN_TOKEN` — bootstrap-токен администратора. Он нужен только чтобы создать
первый клиентский токен; дальше им можно не пользоваться, но удалять из env нельзя:
сервер сверяет его при каждом запросе с этим scope.

Опционально для генерации карточек через LLM:

```dotenv
BRAIN_LLM_BASE_URL=https://openrouter.ai/api/v1
BRAIN_LLM_API_KEY=sk-or-v1-...
BRAIN_LLM_MODEL=openai/gpt-6-luna
BRAIN_LLM_MAX_INPUT_CHARS=24000
```

OpenRouter — OpenAI-совместимый endpoint, отдельный SDK не нужен: сервер сам
добавляет `/chat/completions` к `BRAIN_LLM_BASE_URL` и отправляет bearer-ключ.
`BRAIN_LLM_BASE_URL` и `BRAIN_LLM_MODEL` задаются только вместе: если задан один,
сервер не стартует. `BRAIN_LLM_MAX_INPUT_CHARS` — сколько символов сессии попадает
в промпт (защита контекста и стоимости); можно не задавать, по умолчанию 24000.

Если LLM не задан, сервер работает: карточки остаются heuristic, поиск работает.

## 2. Создать приложение в Dokploy

1. **Create Project** → **Compose**.
2. Provider **GitHub**, репозиторий `SeRGei93/meme`, branch `main`.
3. **Compose Path**: `/docker-compose.yml`.
4. Trigger: **On Push** (Autodeploy можно держать выключенным и деплоить кнопкой).
5. **Environment** — вставить переменные из `.env.example` с реальными значениями.
6. **Deploy**.

Docker build идёт из `server/`, поэтому `server/Dockerfile` и `server/.dockerignore`
используются как есть. `BRAIN_ADMIN_TOKEN` обязателен: без него контейнер упадёт на
старте с понятной ошибкой в логах.

## 3. Домен и TLS

В приложении → **Domains** → **Add Domain**:

- Service: `brain`
- Container port: `8080`
- Domain: ваш домен, например `brain.example.com`
- Certificate: **Let's Encrypt**
- HTTPS: включён

Dokploy/Traefik сам выпустит сертификат после того, как DNS A/AAAA записи домена
указывают на сервер. Порт 8080 наружу не публикуется (`expose`, не `ports`).

## 4. Проверка

```bash
DOMAIN=https://brain.example.com

curl -fsS "$DOMAIN/health" | jq
# {"status":"ok","version":"dev","schema_version":3,...,"database":{"status":"ok","fts5":true}}

curl -sS -o /dev/null -w '%{http_code}\n' "$DOMAIN/api/v1/stats"          # 401
curl -sS "$DOMAIN/api/v1/stats" -H "Authorization: Bearer $BRAIN_ADMIN_TOKEN" | jq
```

Ожидаемо: `/health` → `200`, `database.status=ok`, `fts5=true`; без токена → `401`.
Сертификат: `curl -vI "$DOMAIN/health"` без ошибок TLS.

## 5. Создать клиентские токены

```bash
DOMAIN=https://brain.example.com
ADMIN=$BRAIN_ADMIN_TOKEN

# OpenCode plugin (ingest). mcp:read добавится, когда выйдет #13.
curl -fsS -X POST "$DOMAIN/api/v1/clients" \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name":"opencode-macbook","kind":"opencode","scopes":["ingest"]}' | jq

# macOS app
curl -fsS -X POST "$DOMAIN/api/v1/clients" \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name":"second-brain-app","kind":"ui","scopes":["ui","admin"]}' | jq

# Jarvis
curl -fsS -X POST "$DOMAIN/api/v1/clients" \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name":"jarvis","kind":"jarvis","scopes":["ingest","mcp:read","mcp:write"]}' | jq
```

Токен показывается один раз в ответе (`token`). Сохранить его в Keychain приложения
или в `~/.config/second-brain/mcp_token` (0600) для OpenCode MCP.

## 5a. MCP для OpenCode

После деплоя сервера с `/mcp` (тикет #13) OpenCode подключается к тому же домену:

```bash
install -m 600 /dev/null ~/.config/second-brain/mcp_token
printf '%s' '<opencode-token>' > ~/.config/second-brain/mcp_token
```

```jsonc
// ~/.config/opencode/opencode.jsonc
{
  "mcp": {
    "servers": {
      "second-brain": {
        "type": "remote",
        "url": "https://meme.aimerce.by/mcp",
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

Токену `kind=opencode` нужен scope `mcp:read` для `search`/`get_record`; добавьте
`mcp:write`, если хотите вызывать `remember` из OpenCode. Полный контракт —
[`docs/mcp-tools.md`](mcp-tools.md).

## 6. Персистентность и редеплой

SQLite лежит в `/data/brain.db` на volume `brain-data`.

Проверка:

```bash
# до редеплоя
curl -fsS "$DOMAIN/health" | jq '.schema_version'

# Dokploy → Deploy (или push в main при включённом Autodeploy)

# после
curl -fsS "$DOMAIN/health" | jq '.schema_version'
```

Записи, клиенты и очередь должны сохраниться. **Fresh Volumes** в Dokploy удаляет
volume вместе с базой — не использовать в обычном деплое.

Бэкапы в S3 появятся в #28; до этого единственная защита — volume на сервере.

## 7. Одна реплика, stop-first

В compose зафиксировано `deploy.replicas: 1` и `deploy.update_config.order: stop-first`.
Docker Compose (не Swarm) пересоздаёт контейнер последовательно, поэтому старый
процесс останавливается до старта нового; `stop_grace_period: 30s` даёт серверу
слить HTTP и фоновые циклы. Не масштабировать сервис вручную: SQLite не рассчитан
на несколько писателей.

## 8. Если что-то не так

| Симптом | Что смотреть |
|---|---|
| Deploy падает сразу | Logs: `BRAIN_ADMIN_TOKEN is required` или неверный `BRAIN_LLM_*` |
| 502 от домена | Контейнер unhealthy; `docker ps`, `/health` внутри контейнера |
| 401 на все запросы | Токен отозван/неверный; создать новый admin-токеном |
| Сертификат не выпускается | DNS домена, порт 80/443 на сервере, логи Traefik в Dokploy |
| Данные пропали после редеплоя | Проверить, что volume `brain-data` смонтирован в `/data` |
