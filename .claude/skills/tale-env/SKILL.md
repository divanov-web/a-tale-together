---
name: tale-env
description: Локальная среда A Tale Together в WSL (Ubuntu 24.04 на Windows 11) — проверка и установка Go, PostgreSQL 16 из apt в WSL (Docker как запасной вариант), переменные .env, миграции, сборка, тесты, запуск бота с тестовым токеном, диагностика. Подключать при словах «установи», «запусти», «не собирается», «postgres не поднимается», «миграции», «docker», «env», «make», «go: command not found».
argument-hint: "[check | setup | apply | migrate | reset-db]"
---

# Среда разработки

Пошаговая установка для человека: `docs/dev/environment.md`. Здесь факты и действия для Claude. Прод-сервер (ssh `fornex-dev`, сервис `tale-bot`): `docs/dev/server.md`, ADR-0040; релиз туда — только `/tale-release` по команде руководителя (ADR-0041); доступы и состояние машины — проект «Дом», карточка `fornex`.

## Факты

- WSL Ubuntu 24.04; passwordless sudo нет: команды с `sudo` отдавать руководителю для запуска через `! <команда>`.
- Go 1.27 в `~/.local/go/bin` (установлен без sudo), PATH в `~/.bashrc`. Windows Go 1.25 (`/mnt/c/Program Files/Go`) не используется.
- Postgres 16 установлен из apt (кластер `16 main`, порт 5432, `pg_lsclusters`). Роль и базы `tale`, `tale_test` создаются один раз командой из `docs/dev/environment.md` (нужен sudo → руководитель). Запасной вариант: Docker Desktop с WSL-интеграцией, `docker compose up -d db`.
- `.env` по `.env.example`, в git не попадает. Обязательные: `TELEGRAM_BOT_TOKEN`, `DATABASE_URL`; `BOT_OWNER_ID` для команд `/llm`; ключи провайдеров LLM по `docs/llm/providers.md` (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, для `claude-cli` путь к `claude` и пустая рабочая папка `CLAUDE_CLI_WORKDIR`). Значения из `.env` в чат не выводить.
- Команды Claude: `make check build start stop restart logs test test-integration lint migrate migrate-down migrate-status fmt tools wipe` (`wipe` — по `/tale-wipe`, только по просьбе руководителя). Бот для разработки живёт в фоне (pid `bin/bot.pid`, лог `logs/bot.log`); после правок кода Claude сам делает `make restart` и проверяет `make logs` (правило руководителя 2026-10-10, см. `tale-team`). `make run` (в терминале) — только руководитель. Версия в `/ping` из `git describe` через `-ldflags`. Журнал чатов `logs/chats/<chat_id>/<день>.jsonl` (`CHAT_LOG_DIR`, ADR-0012) читается по `/tale-debug`.

## check

`make check` (Go, `.env`, токен через getMe, Postgres, goose); затем `make build`, `make test`, `make migrate-status`. В терминале GoLand `GOPATH` подставляет IDE: при появлении `~/Go/pkg` или `~/go` проверить Settings → Go → GOPATH (`~/.local/share/go`).

## setup

1. Go отсутствует → `curl -sL https://go.dev/dl/go1.27.2.linux-amd64.tar.gz | tar -xz -C ~/.local` и PATH.
2. Postgres: кластер запущен (`pg_lsclusters`), роль и базы по `docs/dev/environment.md`.
3. `cp .env.example .env`; токены заполняет руководитель.
4. `make migrate`, `make test`, `make start`; `/ping` в песочнице и в личке.

## apply

После правок кода: `make restart`, затем `make logs` — должно быть `bot starting` с новой версией, `scheduler started`, `delivery started`, без `409 Conflict`. После миграций (делает Claude): `make migrate` для `tale` и `goose ... tale_test up` для тестовой базы; затем `psql` по существующим играм — ключи `settings` и значения dev-режима должны соответствовать коду (новые ключи добавляет миграция, но dev-умолчания вроде кворума 1 на старые игры не распространяются — править `UPDATE` вручную и показать результат).

## Тестовый бот

Отдельный бот от BotFather для разработки; группа-песочница; токен только в `.env`. Прод-токены в репозитории не хранятся.

Длительность раунда в dev-игре: `round_minutes` не ниже 2 (обычно 2–3) — ни через `/set`, ни `UPDATE games`, ни в сценариях прогонов, и руководителю значение 1 не предлагать (правило руководителя 2026-10-11): дедлайн округляется вниз до целой минуты (B12), и при 1 минуте раунд длится от 0 до 60 с, планировщик может закрыть его сразу. `/set` единицу отклоняет (`MinRoundMinutesDev` = 2), БД допускает (миграция `00014`), это не повод её ставить через SQL.

## Диагностика

| Симптом | Действие |
|---|---|
| `go: command not found` | `export PATH=$HOME/.local/go/bin:$PATH`; проверить `~/.bashrc` |
| `password authentication failed for user "tale"` | роль не создана: команда из `docs/dev/environment.md` через `!` |
| `connection refused` к БД | `pg_lsclusters` → кластер `down`: `! sudo pg_ctlcluster 16 main start` |
| миграция упала | `make migrate-status`; миграцию не править, писать новую |
| Telegram 409 Conflict | запущен второй экземпляр бота с тем же токеном (`pgrep -a bot`); `make stop` снимает и фоновый, и запущенный без pid-файла, затем `make start` |
| `getMe: Unauthorized` при старте | токен в `.env` неверный или отозван в BotFather |
| бот молчит на `/ping` в группе | privacy mode не мешает командам; проверить, что команда адресована этому боту (`/ping@a_tale_together_dev_bot`) и лог `command` |
