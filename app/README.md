# SecondBrain.app

SwiftUI-приложение: окно, menu bar, Settings, launch at login. Минимальная
macOS 14.0. Проект — `SecondBrain.xcodeproj` (source of truth для сборки).

## Требования

- Xcode 15+ (проверено на Xcode 27.1).
- Один раз принять лицензию и переключить toolchain:

  ```bash
  sudo xcodebuild -license accept
  sudo xcodebuild -runFirstLaunch
  sudo xcode-select -s /Applications/Xcode.app/Contents/Developer
  ```

- В Xcode: **Settings → Accounts** добавить Apple ID, затем в проекте
  **Signing & Capabilities** выбрать Team. Без Team сборка с подписью невозможна;
  CI/локальная проверка могут собирать с `CODE_SIGNING_ALLOWED=NO`.

## Открыть и запустить

```bash
open app/SecondBrain.xcodeproj
```

Схема `SecondBrain` содержит:

- `SecondBrain` — приложение (Debug/Release);
- `SecondBrainTests` — unit-тесты;
- `SecondBrainUITests` — UI smoke test.

UI-тесты запускают отдельный runner и требуют подписанной сборки с выбранным
Team. В `project.yml` уже указан Personal Team; на другой машине выберите свой
Team в Signing & Capabilities.

```bash
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer \
  xcodebuild -project app/SecondBrain.xcodeproj -scheme SecondBrain \
  -destination 'platform=macOS' test
```

Закрытие окна не завершает приложение: оно остаётся в menu bar. Выход — через
menu bar или кнопку Quit.

## Ядро

`app/SecondBrain/Core`:

- `APIClient` — `URLSession` + async/await, типизированные `GET /health`,
  `/api/v1/search`, `/records/{id}`, `/projects`, `/stats`, `/jobs`, `/clients`,
  `/graph`;
- `APIModels` — `Codable`-модели контрактов (`Health`, `Card`, `Record`,
  `SearchResponse`, `Project`, `Stats`, `IngestJob`, `ClientInfo`, `MemoryGraph`,
  произвольный `JSONValue`);
- `APIError` — разбор `ErrorEnvelope`, флаги `isRetryable` и
  `requiresReauthentication` для `401/403`;
- `KeychainStore` — bearer-токены в Keychain по аккаунтам `opencode` и `ui`;
  legacy-ключ `jarvis` удаляется при успешном onboarding;
- `ConfigStore` — атомарная запись `~/.config/second-brain/config.json`
  (каталог `0700`, файл `0600`, ключи snake_case, общие с плагином);
- `PluginInstaller` — копирование встроенного плагина в
  `~/.config/opencode/plugins/second-brain/`, статус и атомарная переустановка;
- `OpenCodeConfig` — разбор JSON/JSONC и merge `mcp.servers.second-brain` в
  глобальный `opencode.json(c)` без потери чужих настроек;
- `SpoolBundle` — строгая валидация `docs/spool.schema.json`, лимиты и тело
  `POST /api/v1/ingest` без server-owned полей;
- `SpoolWatcher` — `DispatchSource` на каталог spool, atomic rename,
  существующие файлы при старте, игнор `*.tmp` и скрытых файлов;
- `UploadQueue` — Swift actor: группировка по `external_id`, самый свежий бандл,
  `Idempotency-Key = bundle_id`, retry с full jitter, `failed/`, счётчики;
- `AppState` — `@Observable`-состояние подключения, ошибок и «нужен новый токен»;
  read-only клиент использует `ui`-токен, загрузка — `opencode`-токен.

`AppState.checkHealth(using:)` не требует токена; `loadStats(using:)` проверяет,
что сохранённый токен ещё работает.

## Onboarding

При первом запуске без `config.json` приложение показывает форму: URL сервера и
bootstrap admin token (`BRAIN_ADMIN_TOKEN`). По кнопке Connect оно:

1. проверяет `GET /health` (degraded `503` отклоняется);
2. создаёт двух клиентов (если имя занято — с уникальным суффиксом, чужие
   токены не отзываются):
   - `opencode-<host>` — `agent`, `ingest`, `mcp:read`, `mcp:write`;
   - `second-brain-app` — `ui`, `admin`.
   Серверный Jarvis не создаётся: его токен живёт в секрете Jarvis, а не в
   Keychain приложения; устаревший ключ `jarvis` удаляется;
3. проверяет выданный `ui`-токен через `GET /api/v1/stats`;
4. сохраняет токены в Keychain, пишет `config.json` (merge с настройками
   плагина, в `app_clients` запоминаются id) и `mcp_token` (`0600`);
5. только после этого отзывает клиентов, созданных этим приложением ранее;
6. best-effort ставит OpenCode plugin и прописывает MCP-сервер. Ошибка прав или
   повреждённый `opencode.json` не отменяет подключение — она видна в Settings
   и в главном окне.

При сбое на любом шаге Keychain, `config.json` и `mcp_token` восстанавливаются,
а новые клиенты отзываются. Если не удалось отозвать старого клиента, его id
попадает в `pending_revocations` и повторяется при следующем запуске.

Admin token не сохраняется. Повторный запуск сразу подключается; Settings →
Tokens → «Повернуть токены…» открывает форму снова и ротирует клиентов, созданных этим приложением (`opencode`, `ui`).

## OpenCode plugin и Settings

Приложение — единственный установщик локальной части:

- плагин лежит внутри `.app` (`Contents/Resources/SecondBrainPlugin`) и
  копируется в `~/.config/opencode/plugins/second-brain/`;
- глобальные плагины OpenCode находит автоматически; `plugins` в конфиге не
  нужен, если используется стандартный путь;
- установка атомарная: новый каталог готовится рядом и подменяет старый только
  после успешного копирования. Повторная установка перезаписывает файлы и не
  трогает другие плагины;
- статус сравнивает содержимое установленных файлов с bundle: «Установлен»,
  «Не установлен», «Нужно обновить», «Ошибка»;
- MCP-сервер пишется в `~/.config/opencode/opencode.json` (или существующий
  `.jsonc`) как `mcp.servers.second-brain` с `type: remote`, `oauth: false`,
  `codemode: false` и заголовком
  `Authorization: Bearer {file:~/.config/second-brain/mcp_token}`. Токен в файл
  не попадает. Чужие ключи и серверы сохраняются. JSONC с комментариями
  читается; если файл нужно переписать, комментарии и форматирование
  нормализуются в валидный JSON. Уже корректный файл не перезаписывается.

Settings показывает и меняет:

- server URL (с проверкой `/health` и обновлением MCP);
- наличие токенов и кнопку ротации;
- spool path (создаётся с `0700`) и launch at login;
- статус плагина, статус MCP и кнопку установки/переустановки.

Для UI-тестов и нестандартных установок доступны переменные окружения
`SECOND_BRAIN_OPENCODE_DIR` (каталог OpenCode), `SECOND_BRAIN_PLUGIN_SOURCE`
(каталог с файлами плагина), `SECOND_BRAIN_UI_TOKEN` (подменить `ui`-токен в
Debug), `SECOND_BRAIN_UI_MOCK=1` (in-process mock API), `SECOND_BRAIN_UI_QUERY`,
`SECOND_BRAIN_UI_RAW_QUERY` (стартовый запрос и raw-фильтр), `SECOND_BRAIN_UI_SECTION`
(стартовый раздел), `SECOND_BRAIN_UI_TOKENS` (подменить наличие токенов),
`SECOND_BRAIN_UI_GRAPH` (`empty`/`error` — состояние графа) и
`SECOND_BRAIN_UI_MENU_QUERY` (стартовый запрос в menu bar) для UI-тестов.

## Поиск и карточка

Главное окно — двухколоночный интерфейс: слева навигация (Поиск, Обзор,
Проекты, Агенты, Клиенты, Граф), статус, очередь загрузки и настройки; справа —
выбранный раздел.

- Запрос отправляется с debounce 300 мс; пустая строка не делает запрос.
- Фильтры: проект (из `/api/v1/projects`), источник с количеством записей
  (из `stats.by_source`; список динамический) и дата (`сегодня`, 7/30/90 дней).
  Параметры уходят в `GET /api/v1/search`.
- `⌘K` фокусирует поле, `↑`/`↓` переключают результаты, `Esc` очищает запрос,
  когда поле в фокусе. Мышь работает так же.
- Результаты — карточки с источником, проектом, датой, summary и keywords.
  Первый результат выбирается автоматически и подгружает полную запись через
  `GET /api/v1/records/{id}`.
- Detail показывает title/summary/questions/keywords/atoms, проект, источник,
  дату и raw_text. В raw_text есть поиск по строкам с числом совпадений и
  кнопка копирования полного текста в clipboard.
- Есть состояния idle/loading/empty/error с retry; цвета и материалы системные,
  поэтому тёмная и светлая темы поддерживаются без отдельных палитр.
- Ключевые элементы имеют accessibility labels и identifiers; одинаковые
  identifiers используются UI-тестами (`search.*`, `record.*`).

## Обзор, проекты и агенты

- **Обзор** показывает количество записей (финальные/черновики), проектов и
  активных клиентов, очередь сервера и локальную очередь, последнюю активность
  и список неудачных ingestion-jobs с замаскированным текстом ошибки. Пустая
  память и отсутствие ошибок имеют отдельные состояния.
- **Проекты** — список из `/api/v1/projects` с количеством записей, путём и
  последней активностью. Клик по проекту переключает на поиск и выставляет
  фильтр `project_key`; фильтр виден плашкой и сбрасывается отдельной кнопкой.
- **Агенты** строит карточку на каждый `source` из `stats.by_source` и списка
  клиентов: количество записей, последнюю активность по свежей job, статус
  job, активных/отозванных клиентов и состояние токена. Для источника,
  созданного bundled-плагином OpenCode, дополнительно видны плагин, MCP и
  последняя загрузка; ниже — очередь сервера и локальная очередь с кнопкой
  отправки.
- **Клиенты** показывает всех клиентов из `/api/v1/clients`: имя, kind, source,
  scopes, создание, последнюю активность и статус. Активные идут выше
  отозванных. Токен приложения помечен и защищён от отзыва; остальные можно
  отозвать после подтверждения — токен перестаёт работать сразу.
- Данные читаются `ui`-токеном (`ui` + `admin`), запросы идут асинхронно и не
  блокируют окно. Ошибки загрузки статистики, jobs или клиентов показываются
  отдельно и не скрывают уже полученные данные.

## Граф памяти

- Граф строится из `GET /api/v1/graph` (до 400 узлов) и рисуется одним
  `Canvas` для связей плюс отдельными кнопками-узлами, поэтому каждый узел
  кликабелен и доступен через VoiceOver.
- Force-directed layout (Fruchterman–Reingold) детерминирован и выполняется в
  `Task.detached`, а не на main actor; для больших графов число итераций
  снижается, интерфейс всё это время остаётся отзывчивым.
- Фильтры по проекту (`project_key`) и источнику (`source`) уходят на сервер;
  легенда и цвета строятся из источников в ответе и стабильны между запусками
  (детерминированный хеш по палитре).
- Клик по узлу открывает карточку записи в sheet поверх графа; загрузка,
  ошибка и повтор используют те же состояния, что и поиск.
- Пустой граф, граф без узлов после фильтра, ошибка загрузки и усечённый
  ответ сервера имеют отдельные подписи.

## Menu bar

- Menu bar extra открывается как окно (`.menuBarExtraStyle(.window)`) и не
  требует главного окна.
- Иконка в строке меню отражает состояние очереди: обычная, отправка, ошибки
  или «нужен новый токен».
- В панели видны статус подключения, счётчики очереди, последняя ошибка
  загрузки и кнопка «Отправить сейчас». Состояние очереди, его цвет и иконка
  берутся из общего `UploadQueuePresentation`, поэтому панель, sidebar и
  строка меню не расходятся.
- Быстрый поиск делает debounce-запрос к `/api/v1/search` (8 результатов);
  клик по результату открывает главное окно и карточку записи, даже если окно
  было закрыто.
- «Обзор» переключает главное окно на Dashboard, «Открыть окно» показывает его
  без смены раздела; «Настройки…» открывает Settings.

## Spool watcher и UploadQueue

После подключения приложение запускает watcher на каталоге из `spool_dir`
(по умолчанию `~/.config/second-brain/spool`):

1. при старте и на каждое изменение (включая atomic rename `*.tmp -> *.json`)
   watcher будит `UploadQueue`;
2. очередь валидирует бандлы по `docs/spool.schema.json` и лимитам (10 MiB,
   10 000 сообщений, 200 000 символов в сообщении);
3. бандлы группируются по `external_id`; отправляется самый свежий
   (`updated_at`), после успеха вся группа удаляется из spool;
4. `Idempotency-Key` всегда `bundle_id`, поэтому retry после timeout/5xx
   переигрывает запрос, а не создаёт дубль;
5. любой `2xx` — удалить; `401/403` — остановить очередь и показать «нужен
   новый токен»; `408/425/429/5xx/network` — оставить и повторить с exponential
   backoff и `Retry-After`; остальные `4xx` и невалидные бандлы — в
   `spool/failed/`. Если сервер отверг самый свежий бандл сессии, вся группа
   уходит в `failed/`, чтобы старая версия не перезаписала запись;
6. если сервер выключен, файлы остаются; при восстановлении или при
   `Отправить сейчас` очередь досылает их.

В главном окне — панель «Загрузка» с тремя счётчиками (в очереди, отправлено
в текущей сессии, ошибки), текущим состоянием, последней ошибкой, временем
последней отправки и обратным отсчётом до повтора. Кнопки «Отправить сейчас» и
«Очистить failed» работают без перезапуска, а при `401` панель предлагает
ротацию токенов. Пустая очередь показывает отдельное состояние. При старте
подхватываются файлы, накопленные при закрытом приложении.

## Подпись и sandbox

- Bundle id: `com.serg.secondbrain`.
- Team в `project.yml` — `9NYF3M7567` (Personal Team). Для другого аккаунта
  поменяйте `DEVELOPMENT_TEAM` и перегенерируйте проект.
- Hardened Runtime включён, App Sandbox выключен (нужен доступ к
  `~/.config/opencode` и `~/.config/second-brain`).
- Launch at login — `SMAppService.mainApp`; при первом включении macOS может
  попросить подтверждение в System Settings → General → Login Items.
  Неподписанная dev-сборка может не зарегистрироваться — ошибка показывается
  в Settings.

## Генерация проекта

`project.yml` — декларативное описание targets/schemes. После изменения состава
файлов, targets или настроек:

```bash
cd app
xcodegen generate
```

`SecondBrain.xcodeproj` коммитится, поэтому обычная работа в Xcode не требует
XcodeGen. Установка: `brew install xcodegen`.

## Сборка из CLI

```bash
DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer \
  xcodebuild -project app/SecondBrain.xcodeproj \
  -scheme SecondBrain -configuration Debug \
  CODE_SIGNING_ALLOWED=NO build
```
