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
  `/api/v1/search`, `/records/{id}`, `/projects`, `/stats`;
- `APIModels` — `Codable`-модели контрактов (`Health`, `Card`, `Record`,
  `SearchResponse`, `Project`, `Stats`, произвольный `JSONValue`);
- `APIError` — разбор `ErrorEnvelope`, флаги `isRetryable` и
  `requiresReauthentication` для `401/403`;
- `KeychainStore` — bearer-токены в Keychain по аккаунтам `admin`, `ui`, `mcp`;
- `ConfigStore` — атомарная запись `~/.config/second-brain/config.json`
  (каталог `0700`, файл `0600`, ключи snake_case, общие с плагином);
- `AppState` — `@Observable`-состояние подключения, ошибок и «нужен новый токен».

`AppState.checkHealth(using:)` не требует токена; `loadStats(using:)` проверяет,
что сохранённый токен ещё работает.

## Onboarding

При первом запуске без `config.json` приложение показывает форму: URL сервера и
bootstrap admin token (`BRAIN_ADMIN_TOKEN`). По кнопке Connect оно:

1. проверяет `GET /health` (degraded `503` отклоняется);
2. отзывает активных клиентов с теми же именами и создаёт заново:
   - `opencode-<host>` — `ingest`, `mcp:read`, `mcp:write`;
   - `jarvis` — `ingest`, `mcp:read`, `mcp:write`;
   - `second-brain-app` — `ui`, `admin`;
3. проверяет выданный `ui`-токен через `GET /api/v1/stats`;
4. сохраняет токены в Keychain, пишет `config.json` (merge с настройками
   плагина) и `mcp_token` (`0600`).

Admin token не сохраняется. Повторный запуск сразу подключается; Settings →
Connection → «Повернуть токены…» открывает форму снова и ротирует все три клиента.
`AppState.connect` ничего не пишет локально, пока сервер не подтвердил токены.

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
