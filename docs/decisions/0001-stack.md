# ADR-0001: Стек

- Статус: принято
- Дата: 2026-10-10
- Область: весь репозиторий

## Контекст
Концепт предлагает Go + PostgreSQL + Telegram Bot API + LLM API. Хобби-проект, один разработчик (Claude), стенд в WSL.

## Решение
| Компонент | Выбор | Причина |
|---|---|---|
| Язык | Go 1.27, модуль `taletogether` | концепт, опыт руководителя, один бинарник |
| БД | PostgreSQL 17 | хранилище и очередь (outbox, `SKIP LOCKED`), без брокера |
| Драйвер | `github.com/jackc/pgx/v5` + `pgxpool` | стандарт де-факто, транзакции, типы |
| Миграции | `github.com/pressly/goose/v3`, SQL + `embed` | простые SQL-файлы, применение из приложения |
| Конфиг | `github.com/caarlos0/env/v11` + `github.com/joho/godotenv` | дефолты + env без флагов, см. coding-rules.md |
| Логи | `log/slog` | stdlib, структурные поля |
| Тесты | `testing` stdlib, табличные | без лишних зависимостей |
| Telegram | ADR-0004 | |
| LLM | ADR-0003 | |

## Последствия
`go.mod` фиксирует версии; правила `.claude/rules/go.md`, `postgres.md`. ORM, DI-фреймворки, брокеры сообщений не вводятся до отдельного ADR.
