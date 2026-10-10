# migrations
- Назначение: SQL-миграции goose `NNNNN_<что>.sql`, `-- +goose Up/Down`.
- Правило: после коммита не правятся; `game_id` в каждой игровой таблице; типы PK по ADR-0005.
- Применение: `make migrate`; см. `.claude/rules/postgres.md`.
