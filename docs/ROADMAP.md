# Second Brain — Roadmap

> Статус: план реализации  
> Дата: 2026-11-03  
> Связанная архитектура: [ARCHITECTURE.md](ARCHITECTURE.md)

---

## 1. Область и допущения

### Входит в v1

- Только **OpenCode** и **Jarvis**.
- Go-сервер на Dokploy: REST, MCP, SQLite + FTS5, карточки, очередь.
- Локальное приложение **SecondBrain.app** на SwiftUI, создаётся в **Xcode**.
- Один `.app`-бинарник, без Docker и без фонового демона.
- OpenCode plugin пишет JSON-бандлы в `spool/`.
- Приложение следит за `spool/` и отправляет бандлы на сервер.
- Поиск и генерация карточек — только на сервере.
- Бэкапы: полный `VACUUM INTO` каждые 6 часов в S3 bucket `meme`, 20 копий.
- Восстановление — только CLI на сервере.

### Не входит в v1

- Claude Code, Codex и другие агенты.
- GitHub, Notion, почта.
- Векторный поиск и эмбеддинги.
- Локальный поиск в приложении.
- Web-UI на сервере.
- Backup UI/API.
- Litestream.
- Шифрование бэкапов.

### Ориентир по срокам

Оценка для одного разработчика: **28–42 рабочих дня**, примерно **6–8 недель** без жёсткого дедлайна. С параллельной работой над сервером, плагином и приложением — **5–7 недель**.

Это ориентир, а не обязательство. Оценки уточняются после M0, когда зафиксированы контракты.

### Xcode

- `SecondBrain.xcodeproj` — source of truth.
- App target, test targets и schemes коммитятся в репозиторий.
- SwiftPM-only приложение не подходит: нужен `.app` bundle, signing, notarization и `SMAppService`.
- Сборка: Xcode / `xcodebuild`; Docker для macOS-приложения не используется.
- Минимальная версия — macOS 14.0 (нужен `@Observable`).

---

## 2. Легенда

- `[ ]` — не начато
- `[~]` — в работе
- `[x]` — готово
- `[!]` — заблокировано

---

## 3. Обзор вех

| Веха | Название | Оценка | Результат |
|---|---|---:|---|
| M0 | Контракты и каркасы | 1–2 дня | OpenAPI, spool schema, MCP contract, Xcode/Go skeletons |
| M1 | Сервер: фундамент | 3–5 дней | Go, SQLite/FTS5, auth, health, Docker, Dokploy |
| M2 | Сервер: ingest, карточки, поиск, MCP | 5–7 дней | Рабочая память end-to-end через API/MCP |
| M3 | OpenCode plugin | 3–4 дня | Backfill + live capture в spool |
| M4 | Xcode app: фундамент | 4–5 дней | App shell, Keychain, API client, onboarding |
| M5 | Spool watcher + upload queue | 2–3 дня | Автоматическая отправка бандлов |
| M6 | UI: память и поиск | 5–7 дней | Dashboard, search, record, graph, projects |
| M7 | Jarvis | 1–2 дня | MCP/HTTP `remember` и `search` |
| M8 | Бэкапы в S3 | 1–2 дня | 6 ч, 20 копий, CLI restore |
| M9 | Hardening и релиз | 3–5 дней | E2E, security, notarization, документация |

**Критический путь:** M0 → M1 → M2 → M4 → M5 → M6 → M9.  
M3 можно делать параллельно с M1–M2. M7 — после M2. M8 — после M1/M2.

---

# M0. Контракты и каркасы

**Цель:** зафиксировать интерфейсы, чтобы сервер, плагин и приложение можно было делать параллельно.

### Задачи

- [ ] Зафиксировать решения v1 (OpenCode + Jarvis, spool, S3 `meme`, без backup UI/API).
- [x] Создать `docs/openapi.yaml`:
  - `POST /api/v1/ingest`
  - `POST /api/v1/memories`
  - `GET /api/v1/search`
  - `GET /api/v1/records/{id}`
  - `GET /api/v1/projects`
  - `GET /api/v1/graph`
  - `GET /api/v1/stats`
  - `GET /api/v1/clients`, `POST`, `DELETE`
  - `GET /api/v1/jobs`
  - `GET /health`
- [x] Создать `docs/spool.schema.json`:
  - `bundle_id`, `source`, `external_id`, `project_key`, `project_path`
  - `title`, `final`, `started_at`, `updated_at`
  - `messages[]`, `metadata`
- [x] Создать `docs/mcp-tools.md`:
  - `search`, `get_record`, `remember`, `list_projects`, `stats`
  - входные/выходные схемы, scope
- [x] Зафиксировать коды ошибок, retry-политику, idempotency-key.
- [x] Зафиксировать модель токенов и scope:
  - `ingest`, `mcp:read`, `mcp:write`, `ui`, `admin`.
- [ ] Создать Go module и скелет `cmd/brain`.
- [ ] Создать Xcode project `SecondBrain.xcodeproj`:
  - macOS App target, SwiftUI;
  - minimum macOS 14.0;
  - bundle id `com.serg.secondbrain` (заменить при необходимости);
  - targets: `SecondBrain`, `SecondBrainTests`, `SecondBrainUITests`;
  - Debug/Release configurations.
- [ ] Определить signing:
  - Development для локальной сборки;
  - Developer ID Application + notarization для распространения;
  - App Sandbox **выключен** (нужен доступ к `~/.config/opencode` и `~/.config/second-brain`).
- [ ] Создать `Dockerfile` скелет сервера.

### Acceptance criteria

- OpenAPI и spool schema проверены и лежат в репозитории.
- `go build ./...` проходит.
- Xcode project открывается, пустое приложение запускается.
- Понятно, какие поля и токены нужны каждой стороне.

---

# M1. Сервер: фундамент

**Цель:** развернуть пустой, но рабочий `brain` на Dokploy.

### Задачи

- [ ] Go-сервер:
  - [ ] config из env;
  - [ ] `GET /health`;
  - [ ] SQLite open + миграции;
  - [ ] `PRAGMA journal_mode=WAL`, `foreign_keys`, `busy_timeout`;
  - [ ] проверка FTS5 (`CREATE VIRTUAL TABLE ... fts5`);
  - [ ] auth middleware: bearer → client + scopes;
  - [ ] admin bootstrap token.
- [ ] Docker:
  - [ ] multi-stage build;
  - [ ] distroless/static;
  - [ ] non-root;
  - [ ] healthcheck.
- [ ] Dokploy:
  - [ ] Application из Dockerfile;
  - [ ] domain + Let's Encrypt;
  - [ ] port `8080`;
  - [ ] volume `/data`;
  - [ ] env (`BRAIN_DB`, `BRAIN_ADMIN_TOKEN`, `BRAIN_LLM_*`, `S3_*`, `BACKUP_*`);
  - [ ] одна реплика, stop-first.
- [ ] Логи `slog` в stdout.

### Acceptance criteria

- `https://brain.example.com/health` отвечает `200`.
- SQLite-файл создаётся в volume и переживает редеплой.
- Запрос без токена получает `401`, с неверным scope — `403`.
- FTS5 доступен в контейнере.

---

# M2. Сервер: ingest, карточки, поиск, MCP

**Цель:** рабочая память end-to-end.

### Задачи

- [ ] Схема БД:
  - [ ] `clients`, `projects`, `records`, `ingest_jobs`, `links`;
  - [ ] `records_fts` + triggers.
- [ ] Ingest:
  - [ ] `POST /api/v1/ingest`;
  - [ ] валидация бандла;
  - [ ] idempotency по `(client_id, idempotency_key)`;
  - [ ] вычисление `content_hash`;
  - [ ] upsert по `(client_id, source, external_id)`;
  - [ ] `source` из токена;
  - [ ] `raw_text` из messages;
  - [ ] `is_final`, `revision`.
- [ ] Memories:
  - [ ] `POST /api/v1/memories`;
  - [ ] если карточка передана — не звать LLM;
  - [ ] если только текст — сгенерировать.
- [ ] Mask:
  - [ ] ключи, токены, пароли, private keys;
  - [ ] до LLM и до записи.
- [ ] Queue:
  - [ ] `ingest_jobs`;
  - [ ] worker с retry/backoff;
  - [ ] восстановление очереди после рестарта.
- [ ] Cards:
  - [ ] OpenAI-совместимый клиент;
  - [ ] JSON-схема карточки;
  - [ ] prompt с metadata;
  - [ ] валидация;
  - [ ] эвристический fallback;
  - [ ] запись `card_status`.
- [ ] Finalizer:
  - [ ] `final=false` обновляет raw;
  - [ ] 30 минут тишины → final;
  - [ ] enqueue card.
- [ ] Search:
  - [ ] нормализация запроса;
  - [ ] префиксы для русских словоформ;
  - [ ] `bm25(records_fts, 6,4,4,5,3,1)`;
  - [ ] freshness `0.5^(age/120)`;
  - [ ] top-8;
  - [ ] фильтры project/source/date.
- [ ] API:
  - [ ] `GET /search`, `GET /records/{id}`, `GET /projects`, `GET /graph`, `GET /stats`;
  - [ ] `GET /clients`, `POST /clients`, `DELETE /clients/{id}`;
  - [ ] `GET /jobs`.
- [ ] MCP:
  - [ ] streamable HTTP `/mcp`;
  - [ ] `search`, `get_record`, `remember`, `list_projects`, `stats`;
  - [ ] bearer auth + scopes.
- [ ] Graph:
  - [ ] связи по проекту и пересечению keywords;
  - [ ] ограничение топ-N связей.

### Acceptance criteria

- `POST /ingest` возвращает `202`, запись появляется в БД.
- Повторный ingest с тем же hash не создаёт дубль.
- Секреты не попадают в `raw_text` и в LLM.
- Карточка генерируется и находится поиском.
- `GET /search` возвращает top-8, свежая запись выше старой при равном BM25.
- MCP `search` и `remember` работают с токеном.
- Тесты на ранжирование, маскировку, идемпотентность проходят.

---

# M3. OpenCode plugin

**Цель:** автоматически наполнять spool сессиями OpenCode.

### Задачи

- [ ] Плагин `Plugin.define`:
  - [ ] `setup(ctx)`;
  - [ ] чтение `~/.config/second-brain/config.json`;
  - [ ] `spool_dir` по умолчанию `~/.config/second-brain/spool`.
- [ ] Backfill:
  - [ ] `ctx.session.list({ directory, limit })`;
  - [ ] пагинация `cursor.next`;
  - [ ] `ctx.session.context(sessionID)`;
  - [ ] `ctx.session.get(sessionID)`;
  - [ ] маркер в `ctx.storage`, чтобы не повторять.
- [ ] Events:
  - [ ] `session.idle` + debounce 3–5 минут;
  - [ ] `session.compaction.started` — сразу;
  - [ ] `session.execution.failed` — final;
  - [ ] `session.execution.interrupted` — final.
- [ ] Normalizer:
  - [ ] user/assistant текст;
  - [ ] tool calls: имя, команда, путь;
  - [ ] удаление reasoning и огромных tool outputs;
  - [ ] стабильные `message.id`.
- [ ] Spool writer:
  - [ ] атомарная запись `*.tmp` → rename;
  - [ ] имя `timestamp-sessionID-uuid.json`;
  - [ ] `final` флаг;
  - [ ] `project_key` из `ctx.location.project` / git.
- [ ] Cursor/outbox:
  - [ ] `ctx.storage` для last message/hash;
  - [ ] при ошибке записи не двигать курсор.
- [ ] Optional `/remember` command:
  - [ ] `ctx.command.transform`;
  - [ ] отправка текста в `/api/v1/memories`.
- [ ] Документация установки:
  - [ ] путь плагина;
  - [ ] запись в `~/.config/opencode/opencode.json(c)`;
  - [ ] MCP config.

### Acceptance criteria

- Новая сессия OpenCode после `idle` создаёт бандл в spool.
- `PreCompact` создаёт бандл до сжатия.
- Backfill не создаёт дублей при повторном запуске.
- Бандл валиден по `spool.schema.json`.
- При закрытом приложении бандлы накапливаются и не теряются.

---

# M4. Xcode app: фундамент

**Цель:** собираемое в Xcode приложение с onboarding и API-клиентом.

> Приложение делается именно в Xcode: `SecondBrain.xcodeproj` — source of truth. Не SwiftPM-only.

### Задачи

- [ ] Xcode project:
  - [ ] macOS App, SwiftUI lifecycle;
  - [ ] `SecondBrain` target;
  - [ ] `SecondBrainTests`;
  - [ ] `SecondBrainUITests`;
  - [ ] minimum macOS 14.0;
  - [ ] Debug/Release;
  - [ ] `Info.plist`;
  - [ ] Hardened Runtime;
  - [ ] App Sandbox off;
  - [ ] signing/team.
- [ ] App shell:
  - [ ] `@main` App;
  - [ ] `WindowGroup`;
  - [ ] `MenuBarExtra`;
  - [ ] закрытие окна не завершает приложение;
  - [ ] `Settings` scene.
- [ ] Core:
  - [ ] `APIClient` на `URLSession` + async/await;
  - [ ] `Codable` модели;
  - [ ] `KeychainStore`;
  - [ ] `ConfigStore`;
  - [ ] `AppState` `@Observable`;
  - [ ] обработка ошибок и `401`.
- [ ] Onboarding:
  - [ ] ввод server URL;
  - [ ] ввод admin token;
  - [ ] `GET /health`;
  - [ ] создание/ротация токенов `opencode`, `jarvis`, `ui`;
  - [ ] запись `~/.config/second-brain/config.json`;
  - [ ] запись `mcp_token` с `0600`;
  - [ ] установка OpenCode plugin;
  - [ ] правка/подсказка MCP config;
  - [ ] проверка подключения.
- [ ] Launch at login:
  - [ ] `SMAppService.mainApp`.
- [ ] Базовые unit-тесты API client и Keychain.

### Acceptance criteria

- Проект собирается в Xcode без ошибок.
- Приложение запускается, показывает onboarding.
- Onboarding успешно подключается к серверу и создаёт токены.
- Токены лежат в Keychain; файлы `0600`.
- Плагин установлен в `~/.config/opencode/plugins/second-brain/`.
- MCP config валиден.

---

# M5. Spool watcher + upload queue

**Цель:** автоматическая и надёжная отправка бандлов.

### Задачи

- [ ] `SpoolWatcher`:
  - [ ] `DispatchSource`/FSEvents на `spool/`;
  - [ ] обработка atomic rename;
  - [ ] обработка уже существующих файлов при старте;
  - [ ] игнор `*.tmp`.
- [ ] `UploadQueue` (Swift actor):
  - [ ] группировка по `external_id`;
  - [ ] выбор самого свежего бандла;
  - [ ] последовательная/ограниченная параллельная отправка;
  - [ ] retry с exponential backoff;
  - [ ] обработка `202`, `4xx`, `5xx`, `429`;
  - [ ] удаление бандлов после успеха;
  - [ ] `failed/` для невалидных;
  - [ ] ограничение размера payload.
- [ ] UI очереди:
  - [ ] pending/sent/failed;
  - [ ] last error;
  - [ ] retry now;
  - [ ] очистка failed.
- [ ] Offline:
  - [ ] сервер недоступен → файлы остаются;
  - [ ] при старте очередь подхватывается.

### Acceptance criteria

- Новый бандл уходит на сервер в течение нескольких секунд.
- При выключенном сервере бандлы копятся и отправляются после восстановления.
- Нет дублей при повторной отправке.
- После успеха spool очищается.
- Ошибки видны в UI.

---

# M6. UI: память и поиск

**Цель:** полноценный интерфейс к серверной памяти.

### Задачи

- [ ] Dashboard:
  - [ ] количество записей/проектов/клиентов;
  - [ ] очередь;
  - [ ] последняя активность;
  - [ ] ошибки ingestion.
- [ ] Search:
  - [ ] поле запроса;
  - [ ] debounce;
  - [ ] фильтры project/source/date;
  - [ ] результаты-карточки;
  - [ ] клавиатурная навигация;
  - [ ] `⌘K`.
- [ ] Record detail:
  - [ ] title/summary/questions/keywords/atoms;
  - [ ] project/source/date;
  - [ ] raw_text с поиском;
  - [ ] copy.
- [ ] Projects:
  - [ ] список;
  - [ ] количество записей;
  - [ ] переход к поиску по проекту.
- [ ] Agents:
  - [ ] OpenCode: last capture, plugin status;
  - [ ] Jarvis: last memory, token status;
  - [ ] очередь.
- [ ] Graph:
  - [ ] Canvas + force-directed layout;
  - [ ] узлы/связи;
  - [ ] фильтры;
  - [ ] клик → карточка.
- [ ] Settings:
  - [ ] server URL;
  - [ ] токены;
  - [ ] launch at login;
  - [ ] spool path;
  - [ ] reinstall plugin.
- [ ] Menu bar:
  - [ ] статус;
  - [ ] быстрый поиск;
  - [ ] открыть dashboard.
- [ ] Состояния:
  - [ ] loading/empty/error;
  - [ ] тёмная/светлая тема;
  - [ ] accessibility labels.

### Acceptance criteria

- Поиск работает end-to-end.
- Карточка открывается, raw_text отображается.
- Граф строится и фильтруется.
- Приложение не блокируется на сетевых запросах.
- UI корректен в dark/light mode.

---

# M7. Jarvis

**Цель:** Jarvis пишет в память по команде.

### Задачи

- [ ] Подтвердить основной путь: MCP или HTTP.
- [ ] Токен `jarvis` со scope `mcp:read`, `mcp:write`, `ingest`.
- [ ] MCP:
  - [ ] `remember`;
  - [ ] `search`;
  - [ ] инструкция для Jarvis.
- [ ] HTTP:
  - [ ] `POST /api/v1/memories`;
  - [ ] `GET /api/v1/search`.
- [ ] Source mapping: `source=jarvis` из токена.
- [ ] Идемпотентность по `external_id`.
- [ ] Документация: URL, заголовок, примеры.
- [ ] Проверка: команда «запомни» → запись → поиск находит.

### Acceptance criteria

- Jarvis сохраняет память по команде.
- Запись видна в приложении и находится поиском.
- Отзыв токена блокирует запись.
- Jarvis не может подделать `source`.

---

# M8. Бэкапы в S3

**Цель:** полные бэкапы каждые 6 часов, 20 копий.

### Задачи

- [ ] Конфиг:
  - [ ] `S3_ENDPOINT=https://storage-987.s3hoster.by`;
  - [ ] `S3_REGION=BTC`;
  - [ ] `S3_BUCKET=meme`;
  - [ ] `S3_ACCESS_KEY` / `S3_SECRET_KEY`;
  - [ ] `BACKUP_CRON=0 */6 * * *`;
  - [ ] `BACKUP_KEEP=20`.
- [ ] Backup:
  - [ ] `PRAGMA wal_checkpoint(TRUNCATE)`;
  - [ ] `VACUUM INTO`;
  - [ ] `PRAGMA integrity_check`;
  - [ ] zstd;
  - [ ] SHA-256 в metadata;
  - [ ] `PutObject`;
  - [ ] `ListObjects`;
  - [ ] удаление старше 20;
  - [ ] логирование.
- [ ] CLI:
  - [ ] `brain backup run`;
  - [ ] `brain backup list`;
  - [ ] `brain restore --object ... --db ...`.
- [ ] Pre-migration backup.
- [ ] Проверка restore на тестовой БД.
- [ ] Никакого UI/API для бэкапов.

### Acceptance criteria

- Первый `brain backup run` создаёт объект в `meme/`.
- 21-й бэкап удаляет самый старый.
- `brain backup list` показывает 20 объектов.
- Restore восстанавливает БД и проходит `integrity_check`.
- Секреты S3 не логируются.

---

# M9. Hardening и релиз

**Цель:** стабильный v1.

### Задачи

- [ ] E2E на staging:
  - [ ] OpenCode: backfill + live;
  - [ ] app upload;
  - [ ] card generation;
  - [ ] search;
  - [ ] Jarvis remember;
  - [ ] backup/restore.
- [ ] Security:
  - [ ] token scopes;
  - [ ] 401/403;
  - [ ] rate limit;
  - [ ] payload limit;
  - [ ] secret masking;
  - [ ] no secrets in logs.
- [ ] Reliability:
  - [ ] server restart с очередью;
  - [ ] app restart с бандлами;
  - [ ] SQLite busy/locking;
  - [ ] Dokploy stop-first.
- [ ] Xcode release:
  - [ ] Release archive;
  - [ ] Developer ID signing;
  - [ ] notarization;
  - [ ] staple;
  - [ ] DMG.
- [ ] Документация:
  - [ ] install;
  - [ ] OpenCode setup;
  - [ ] Jarvis setup;
  - [ ] backup/restore;
  - [ ] troubleshooting.
- [ ] Мониторинг:
  - [ ] `/health`;
  - [ ] logs;
  - [ ] alerts на ошибки ingest (по желанию).

### Acceptance criteria

- Полный сценарий проходит на чистой машине.
- Приложение подписано и notarized.
- Бэкап и restore проверены.
- Документация позволяет повторить установку без автора.

---

## 4. Параллельные треки

| Трек | Вехи | Можно начинать после |
|---|---|---|
| Server core | M1, M2, M8 | M0 |
| OpenCode plugin | M3 | M0 (spool schema) |
| Xcode app | M4, M5, M6 | M0 (OpenAPI contract) |
| Jarvis | M7 | M2 (MCP tools) |
| Ops/Release | M1, M8, M9 | M0 |

Пока сервер не готов, приложение может работать с mock API по `openapi.yaml`. Плагин может писать бандлы в spool без сервера.

---

## 5. Тестовая стратегия

### Unit

- Go:
  - FTS5 ranking;
  - freshness;
  - mask;
  - idempotency;
  - card JSON validation;
  - backup rotation.
- OpenCode plugin:
  - normalizer;
  - spool writer;
  - cursor.
- Swift:
  - API client;
  - Keychain;
  - spool watcher;
  - upload queue.

### Integration

- Mock server для app.
- Spool → upload → mock API.
- MCP `search`/`remember` через curl.
- Backup → S3 mock (minio в тестах или локальный S3).

### E2E

- Реальный Dokploy staging.
- OpenCode сессия → карточка → поиск.
- Jarvis → память → поиск.
- Backup → restore.

### Ranking regression

- Golden-набор запросов и ожидаемый порядок.
- Проверка, что свежая запись выше старой при равном BM25.
- Проверка русских словоформ.

---

## 6. Release checklist

- [ ] Dokploy: домен, TLS, volume, env, one replica, stop-first.
- [ ] S3 bucket `meme`, access key, права, `backup run/list`.
- [ ] S3 versioning off или подтверждён lifecycle.
- [ ] OpenCode plugin установлен и доверен.
- [ ] MCP config с `oauth:false` и bearer.
- [ ] Xcode archive, Developer ID, notarization, staple, DMG.
- [ ] Onboarding на чистой macOS.
- [ ] Backup/restore drill.
- [ ] Token rotation/revocation.
- [ ] Документация и troubleshooting.

---

## 7. Риски

| Риск | Вероятность | Влияние | Митигация |
|---|---|---|---|
| OpenCode plugin API изменится | Средняя | Высокое | Тонкий адаптер, backfill, fallback через OpenCode API |
| Xcode signing/notarization | Средняя | Среднее | Настроить в M0–M4, не в конце |
| App Sandbox блокирует `~/.config` | Высокая | Среднее | Non-sandboxed Developer ID app |
| LLM дорого/плохое качество | Средняя | Среднее | Дешёвая модель, heuristic fallback, бюджет |
| SQLite locking в Dokploy | Низкая | Высокое | Один writer, одна реплика, stop-first, WAL |
| S3 versioning включён | Неизвестно | Среднее | Проверить, при необходимости lifecycle |
| Backfill слишком большой | Средняя | Среднее | Throttle, очередь, бюджет карточек |
| Приложение закрыто → spool растёт | Средняя | Низкое | Launch at login, menu bar, индикатор |
| Сеть до сервера медленная | Средняя | Низкое | Top-8, сжатие, таймауты |

---

## 8. Post-v1 backlog

- Claude Code и Codex.
- GitHub, Notion, почта.
- Litestream.
- Шифрование бэкапов.
- Backup UI/API.
- Web dashboard.
- Метрики/алерты.
- Мобильный клиент.
- Улучшение поиска: синонимы, query expansion.
- Экспорт/импорт.

---

## 9. Definition of Done для v1

- OpenCode автоматически наполняет память.
- Jarvis пишет по команде.
- Приложение показывает, ищет и открывает записи.
- MCP `search`/`remember` работают.
- Бэкапы каждые 6 часов, 20 копий, restore проверен.
- Нет прямого доступа к SQLite.
- Секреты не попадают в LLM и БД.
- Приложение собрано в Xcode, подписано и notarized.
- Документация позволяет развернуть систему с нуля.
