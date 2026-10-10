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
Заполнить `TELEGRAM_BOT_TOKEN` (тестовый бот от BotFather), ключи провайдеров LLM по [providers.md](../llm/providers.md). Необязательные: `SCHEDULER_TICK` (период планировщика раундов, 10s), `OUTBOX_TICK` (период воркера доставки, 3s). `.env` в git не попадает.

## Команды
| Команда | Действие |
|---|---|
| `make check` | проверка окружения: Go, `.env`, Postgres, токен бота, goose |
| `make build` | сборка в `bin/bot` |
| `make run` | запуск бота в терминале |
| `make start` / `make stop` / `make restart` | бот в фоне: pid `bin/bot.pid`, лог `logs/bot.log`; выполняет только руководитель, см. [go_commands.md](../go_commands.md) |
| `make logs` | последние строки лога фонового бота |
| `make test` | юнит-тесты |
| `make test-integration` | тесты с БД (`DATABASE_URL_TEST`) |
| `make lint` | `gofmt`, `go vet`, `golangci-lint` при наличии |
| `make migrate` / `migrate-down` / `migrate-status` | миграции goose |
| `make fmt` | форматирование |

## Первый запуск
```bash
make check && make migrate && make start
```
Бот пишет `telegram polling started bot=<username>`. Проверка: `/ping` в группе-песочнице и в личке бота → `pong`, версия, время. Остановка `make stop`. Второй экземпляр с тем же токеном получает в логе `409 Conflict`.

## GoLand
Проект открыт из Windows; SDK указать WSL (`\\wsl$\Ubuntu\home\divanov\.local\go`), build tag `os=linux` уже в `.idea`.
