---
paths:
  - "migrations/**"
  - "internal/adapter/postgres/**"
  - "**/*.sql"
---
# PostgreSQL

- Драйвер `pgx/v5` (`pgxpool`); миграции `goose/v3`, SQL-файлы в `migrations/`, встроены через `embed`, применяются `make migrate` или при старте по флагу конфига.
- Файл миграции `NNNNN_<что>.sql` с `-- +goose Up` / `-- +goose Down`. После коммита миграция неизменяема; исправление — новая миграция.
- Имена: snake_case; таблицы во множественном числе (`games`, `characters`); PK `id`; FK `<сущность>_id`; время `timestamptz`; `created_at`, `updated_at`. Тип PK по ADR-0005.
- Каждая игровая таблица содержит `game_id` с индексом; каждый запрос фильтрует по `game_id`. Запрос без `game_id` к игровой таблице — дефект. Таблицы уровня бота (`users`, `chats`, `chat_moderators`, `telegram_updates`, `llm_profiles`, `app_settings`) без `game_id`.
- Связи внутри БД по `id`; идентификаторы Telegram хранятся только в `users.telegram_id`, `chats.telegram_chat_id`, `messages_outbox.telegram_chat_id` (ADR-0007).
- Идемпотентность: уникальные ключи на внешние события (`telegram_update_id`, `(game_id, round_no)`, `(round_id, character_id)` для заявок); `INSERT ... ON CONFLICT DO NOTHING`.
- Разрешение раунда — одна транзакция; outbox-сообщения пишутся в ней же; воркеры берут работу через `SELECT ... FOR UPDATE SKIP LOCKED`.
- Репозитории: методы с `ctx`, работают через `Querier` из `db(ctx)` (транзакция в контексте после `InTx`, иначе пул; ADR-0008); SQL в константах рядом с методом; ORM нет. Новый метод `Store` дублируется в `adapter/postgres/fake`.
- JSONB для гибких полей (параметры действий, характеристики) с `schema_version`.
- `world_events` только добавляется; состояние мира меняется только через события.
