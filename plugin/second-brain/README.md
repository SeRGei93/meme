# Second Brain — OpenCode plugin

Тонкий TypeScript-плагин OpenCode: собирает нормализованные сессии и складывает
JSON-бандлы в локальный spool. Сеть плагин не использует — отправкой занимается
`SecondBrain.app` (M5).

## Что делает

| Событие | Действие |
|---|---|
| `session.idle` | ждёт debounce (по умолчанию 4 минуты) и пишет live-бандл `final=false` |
| `session.compaction.started` | пишет бандл сразу, до сжатия, `final=false`; hook `compaction` удерживает model call до записи (с одним повтором при ошибке) |
| `session.execution.failed` | пишет бандл сразу, `final=true` |
| `session.execution.interrupted` | пишет бандл сразу, `final=true` |
| первый запуск | backfill существующей истории: `session.list` с пагинацией, `final=true` |

Повторный `idle` сбрасывает таймер. События одной сессии обрабатываются
последовательно. Если содержимое не изменилось, второй бандл не пишется; смена
`final=false` на `final=true` пишется всегда. При выгрузке плагина (закрытие
OpenCode) ожидающий `idle`-бандл сбрасывается на диск, а не теряется.

## Backfill истории

При первом запуске плагин импортирует сессии, созданные до его установки:

- страницы по 50 через `session.list({ directory, parentID: null, limit, cursor })`,
  пока `cursor.next` не станет пустым; импортируются только корневые сессии,
  повторяющийся курсор обрывает обход;
- каждая сессия проходит обычный `get` + `context` + normalize и пишется с
  `final=true`, поэтому per-session курсор и fingerprint не создают дублей;
- между сессиями пауза 200 мс, чтобы не перегружать диск и OpenCode; live-события
  продолжают обрабатываться параллельно;
- сессия, тихая меньше 30 минут, пишется с `final=false`, чтобы серверный
  finalizer закрыл её позже (старые сессии — `final=true`);
- ошибка одной сессии логируется с её id и не останавливает остальные; неудачные
  id сохраняются в маркере, и следующий запуск перечитывает список, но заново
  захватывает только их. После трёх неудач сессия перестаёт блокировать
  завершение backfill;
- прогресс (`nextCursor`, счётчики, неудачные id) хранится в `ctx.storage` под
  `backfill/v1/<hash(directory)>`; завершённый backfill больше не запускается,
  прерванный (закрытие OpenCode) продолжается со следующей страницы; битый
  маркер игнорируется, устаревший курсор один раз перезапускает обход сначала.

Курсор сессии пишется после атомарного `rename`, поэтому жёсткое убийство
процесса между этими шагами может дать повторный бандл; сервер дедуплицирует
бандлы по `external_id`/`content_hash`. Backfill и live-события сериализуются
по `sessionID`, так что одновременный захват одной сессии пишет один бандл.

Нормализация: user/assistant текст и tool calls (`name`, `command`, `path`).
Reasoning и вывод инструментов не сохраняются. Текст и поля обрезаются по
лимитам из `docs/spool.schema.json`.

## Установка

Приложение ставит плагин в `~/.config/opencode/plugins/second-brain/` (M4).
Вручную:

```bash
mkdir -p ~/.config/opencode/plugins
cp -R plugin/second-brain ~/.config/opencode/plugins/second-brain
```

Добавь путь в глобальный `~/.config/opencode/opencode.json(c)`. Плагин — это
каталог с `package.json` и `exports`, поэтому указывается абсолютный путь:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "plugins": ["/Users/<you>/.config/opencode/plugins/second-brain"]
}
```

Конфигурация читается при загрузке плагина. После правки файла выполни `/reload`
(или перезапусти OpenCode), чтобы настройки применились. Зависимость
`@opencode/plugin` ставится в каталог плагина (`npm install --omit=dev` внутри
него), сам OpenCode её не подставляет.

## Конфигурация

`~/.config/second-brain/config.json` (`XDG_CONFIG_HOME` учитывается). Файл
необязателен; при ошибке чтения/парсинга используются значения по умолчанию, чтобы
плагин не переставал собирать сессии.

```json
{
  "spool_dir": "/Users/serg/.config/second-brain/spool",
  "idle_debounce_ms": 240000,
  "max_message_chars": 200000,
  "max_messages": 10000,
  "max_tool_calls_per_message": 500,
  "max_tool_field_chars": 2000
}
```

- `spool_dir` по умолчанию `~/.config/second-brain/spool`; `~` раскрывается.
- `idle_debounce_ms` по умолчанию `240000` (4 минуты, окно 3–5 минут из тикета).
- Остальные лимиты совпадают с `docs/spool.schema.json`; значения больше схемных
  обрезаются до максимума схемы, чтобы бандл оставался валидным.

## Spool

Файл: `<timestamp>-<sessionID>-<bundle_id>.json`, например
`20261103T123456Z-ses_01JABCDEF-9a7c2f5e-1111-4222-8333-444455556666.json`.

Запись атомарная: сначала `*.json.tmp` с правами `0600`, затем `rename`. При
ошибке временный файл удаляется, а курсор сессии не двигается — следующий
`idle` повторит запись. Приложение игнорирует `*.tmp` и дедуплицирует бандлы по
`external_id`.

`project_key` выбирается по приоритету из `docs/spool.schema.json`:

1. git remote как `host/owner/repo` (читается локально из `.git/config`);
2. `opencode:<projectID>`;
3. `local:<hostname>:<project_path>`.

## Разработка

```bash
cd plugin/second-brain
npm install
npm test        # node --test, Node 23+ (TypeScript без сборки)
npm run typecheck
```

Тесты на seam'ах: `loadConfig`, `normalizeSession`, `resolveProjectKey`,
`writeBundle`, `createCapture`, `createEventRouter`, `runBackfill`. Интеграционный тест пишет
бандл реальным writer'ом и валидирует его через `tools/validate-spool.mjs`.

Не входит в этот тикет: команда `/remember` (#16).
