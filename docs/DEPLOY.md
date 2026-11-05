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

## 2. CI/CD: сборка в GitHub Actions, доставка в Dokploy

Сборка идёт **только на GitHub Actions**, сервер ничего не компилирует:

1. Push в `main` (пути `server/**`, `plugin/**`, `tools/**`, compose, workflow).
2. Job `check`: `go vet`/`go test`, контрактные тесты `tools`, тесты и typecheck
   плагина.
3. Job `build`: `docker buildx` собирает `server/Dockerfile` и пушит в GHCR
   `ghcr.io/sergei93/meme-brain:<sha>` и `:latest`.
4. Job `deploy`: после успешной сборки пушит git-тег `deploy-<run_id>`.
   Dokploy с Trigger Type **Tag** + Autodeploy **ON** запускает `compose up` с
   `pull_policy: always` и поднимает уже готовый образ.

Почему тег, а не webhook: в текущей версии Dokploy webhook отвечает
`400 Automatic deployments are disabled`, если Autodeploy выключен, а при
включённом On Push он сработал бы раньше сборки. Тег гарантирует порядок.

### Однократная настройка

1. **Dokploy → Settings → Registry**: добавить `ghcr.io`, username `SeRGei93`,
   PAT с правом `read:packages`. Либо оставить пакет `meme-brain` публичным
   (GitHub → Packages → Package settings → Change visibility) — он уже
   публичный, тогда registry не нужен.
2. **Create Project → Compose**: GitHub `SeRGei93/meme`, branch `main`,
   **Compose Path**: `/docker-compose.yml`.
3. **Trigger Type: Tag**, **Autodeploy: ON**. Push ветки `main` Dokploy
   игнорирует; деплой запускает только тег от workflow.
4. **Environment** — переменные из `.env.example` (см. §1).
5. Первый раз: дождаться зелёного workflow, затем **Deploy** в Dokploy.

`BRAIN_VERSION` больше не нужен: версия в `/health` — это SHA коммита, переданный
в `--build-arg VERSION` при сборке. `BRAIN_ADMIN_TOKEN` обязателен: без него
контейнер упадёт на старте.

## 2a. Обновление и откат

- Обновление: merge в `main` → workflow собирает образ → пушит тег → Dokploy
  деплоит. Вручную: **Deploy** (не Rebuild) — подтянет свежий `:latest`.
- Откат: задать в Dokploy Environment `IMAGE_TAG=<полный sha>` и нажать
  **Deploy**. Workflow публикует тег на каждый коммит.
- Локальная сборка: `docker compose -f docker-compose.local.yml up --build`.

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
# {"status":"ok","version":"<commit-sha>","schema_version":4,...,"database":{"status":"ok","fts5":true}}

curl -sS -o /dev/null -w '%{http_code}\n' "$DOMAIN/api/v1/stats"          # 401
curl -sS "$DOMAIN/api/v1/stats" -H "Authorization: Bearer $BRAIN_ADMIN_TOKEN" | jq
```

Ожидаемо: `/health` → `200`, `database.status=ok`, `fts5=true`; без токена → `401`.
Сертификат: `curl -vI "$DOMAIN/health"` без ошибок TLS.

## 5. Создать клиентские токены

```bash
DOMAIN=https://brain.example.com
ADMIN=$BRAIN_ADMIN_TOKEN

# OpenCode plugin + MCP
curl -fsS -X POST "$DOMAIN/api/v1/clients" \
  -H "Authorization: Bearer $ADMIN" -H "Content-Type: application/json" \
  -d '{"name":"opencode-macbook","kind":"opencode","scopes":["ingest","mcp:read","mcp:write"]}' | jq

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

Приложение `SecondBrain.app` делает эти шаги само в onboarding: введите URL и
`BRAIN_ADMIN_TOKEN`, и оно проверит `/health`, создаст трёх клиентов, сохранит
токены в Keychain, `config.json` и `mcp_token`, а затем отзовёт клиентов,
созданных этим приложением ранее. Чужие клиенты не затрагиваются: при занятом
имени выдаётся клиент с суффиксом. Повторный запуск с настроенным приложением
сразу подключается; Settings → Connection → «Повернуть токены…» ротирует клиентов
заново.

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

# Dokploy → Deploy (подтянет свежий :latest)
# или merge в main → GitHub Actions соберёт образ и вызовет Dokploy webhook.

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
