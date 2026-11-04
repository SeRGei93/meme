# AGENTS.md

## Project

Second Brain — центральная память для OpenCode и Jarvis.

- Сервер: Go, SQLite + FTS5, MCP, Dokploy.
- Локально: SwiftUI `.app`, собирается в Xcode.
- OpenCode plugin пишет бандлы в `spool/`.
- Поиск и карточки — только на сервере.
- Бэкапы: полный снимок SQLite каждые 6 часов в S3 bucket `meme`, 20 копий.

Документы:

- [Архитектура](docs/ARCHITECTURE.md)
- [Roadmap](docs/ROADMAP.md)
- [Контракты v1](docs/CONTRACTS.md)

## Repository layout

- `server/` — Go-сервер: `go.mod`, `cmd/brain`, `internal/`, `Dockerfile`.
- `app/` — SwiftUI-приложение (появится в M4).
- `plugin/` — OpenCode plugin (TypeScript, без сборки; live capture в spool).
- `tools/` — контрактные тесты, mock API, валидатор spool.
- `docs/` — архитектура, roadmap, контракты.

Go-команды запускаются из `server/`: `go test ./...`, `go build ./...`.

## Workflow по тикетам

Источник правды — GitHub Issues в `SeRGei93/meme`, нарезанные из `docs/ROADMAP.md`.

- `ready-for-agent` — можно брать агенту; `ready-for-human` — нужен человек.
- Тикет доступен, когда все его `blocked-by` закрыты.
- Новая сессия начинает с `/next-ticket` или `/next-ticket <номер>`, если пользователь не сказал иначе.
- Перед работой: issue, `docs/ARCHITECTURE.md`, `docs/ROADMAP.md`, `GLOSSARY.md` и ADR, если они есть.
- Реализация — через `@implement`: TDD на согласованных seam'ах и `@code-review`.
- Завершение: acceptance criteria, тесты, PR с `Closes #<n>`; issue закрывается после merge.
- Не начинать тикет с открытыми блокерами и не трогать parent #1.

## Agent skills

### Issue tracker

GitHub Issues в `SeRGei93/meme`. Использовать `gh` CLI. См. `docs/agents/issue-tracker.md`.

### Triage labels

Пять канонических ролей: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. См. `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` и `docs/adr/`. См. `docs/agents/domain.md`.

### Engineering workflow

Скиллы вызываются через `@<id>`; `/next-ticket` — отдельная команда OpenCode.

- `@to-tickets` — разбить roadmap/spec на tracer-bullet issues.
- `@implement` — реализовать issue/spec; внутри вызывает `@tdd` и `@code-review`.
- `@tdd` — red-green-refactor на согласованных seam'ах.
- `@code-review` — review diff по двум осям: Standards и Spec.
- `@diagnosing-bugs` — дисциплинированный цикл диагностики багов.
- `@triage` — разбор и маркировка issue, когда их станет много.
- `@domain-modeling` — вести `GLOSSARY.md` и ADR по мере принятия решений.
