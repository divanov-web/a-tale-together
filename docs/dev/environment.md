# Среда разработки

Windows 11 → WSL Ubuntu 24.04. Всё запускается из WSL. Для Claude: скилл `/tale-env`.

## Go
Установлен без sudo в `~/.local/go` (1.27). Проверка: `go version`. PATH добавлен в `~/.bashrc`.

## PostgreSQL
Установлен из apt: PostgreSQL 16, кластер `16 main` на 5432 (`pg_lsclusters`). Роль и базы создаются один раз (нужен sudo):
```bash
sudo -u postgres psql -c "create user tale with password 'tale';" -c "create database tale owner tale;" -c "create database tale_test owner tale;"
```
Проверка: `psql "postgres://tale:tale@localhost:5432/tale?sslmode=disable" -c 'select 1'`.

Запасной вариант: Docker Desktop с WSL-интеграцией и `docker compose up -d db` (те же роль и базы).

## Конфигурация
```bash
cp .env.example .env
```
Заполнить `TELEGRAM_BOT_TOKEN` (тестовый бот от BotFather), ключи провайдеров LLM по [providers.md](../llm/providers.md). `BOT_OWNER_ID` — Telegram ID владельца (из `/me`), открывает команды `/llm`; `CLAUDE_CLI_PATH` и `CLAUDE_CLI_WORKDIR` — бинарь `claude` и пустая рабочая папка провайдера `claude-cli` (папка создаётся ботом). Необязательные: `SCHEDULER_TICK` (период планировщика раундов, 10s), `OUTBOX_TICK` (период воркера доставки, 3s), `CHAT_LOG_DIR` (журнал чатов, `logs/chats`; пусто — выключен), `CHAT_LOG_RETENTION_DAYS` (30; [chat-logs.md](chat-logs.md)) и `GUIDE_URL` (корень руководства игрока для кнопок-ссылок под `/help`, по умолчанию GitHub репозитория; пусто — без ссылок). `.env` в git не попадает.

## Команды
| Команда | Действие |
|---|---|
| `make check` | проверка окружения: Go, `.env`, Postgres, токен бота, goose |
| `make build` | сборка в `bin/bot` |
| `make run` | запуск бота в терминале |
| `make start` / `make stop` / `make restart` | бот в фоне: pid `bin/bot.pid`, лог `logs/bot.log`; после правок кода Claude делает `make restart` сам, см. [go_commands.md](../go_commands.md) |
| `make logs` | последние строки лога фонового бота; журнал чатов для разбора ошибок — `logs/chats/<chat_id>/<день>.jsonl` ([chat-logs.md](chat-logs.md)) |
| `make test` | юнит-тесты |
| `make test-integration` | тесты с БД (`DATABASE_URL_TEST`); пакеты идут последовательно, потому что две интеграционные папки мигрируют одну базу |
| `make lint` | `gofmt`, `go vet`, `golangci-lint` при наличии |
| `make eval-interpreter` | оценочный набор интерпретатора на живом `claude-cli` (подписка Claude Code; `EVAL_ARGS="-v -case имя -model haiku -effort low"`; модель по умолчанию `sonnet`, как в профиле `local-claude`), порог 90 % |
| `make eval-narrator` | оценочный набор рассказчика на живом `claude-cli` (подписка; те же `EVAL_ARGS`, модель по умолчанию `opus`), порог 90 % |
| `make migrate` / `migrate-down` / `migrate-status` | миграции goose |
| `make fmt` | форматирование |
| `make wipe` | вайп песочницы: удалить все игры и логи, оставить пользователей, чат, настройки и профили LLM (`scripts/wipe-sandbox.sql`, только `APP_ENV=dev`); см. [go_commands.md](../go_commands.md) |

## Первый запуск
```bash
make check && make migrate && make start
```
Бот пишет `telegram polling started bot=<username>`. Проверка: `/ping` в группе-песочнице и в личке бота → `pong`, версия, время. Остановка `make stop`. Второй экземпляр с тем же токеном получает в логе `409 Conflict`.

## GoLand
Проект открыт из Windows; SDK указать WSL (`\\wsl$\Ubuntu\home\divanov\.local\go`), build tag `os=linux` уже в `.idea`.
