---
paths:
  - "migrations/**"
  - "internal/adapter/postgres/**"
  - "**/*.sql"
---
# PostgreSQL

- Драйвер `pgx/v5` (`pgxpool`); миграции `goose/v3`, SQL-файлы в `migrations/`, встроены через `embed`, применяются `make migrate` или при старте по флагу конфига.
- Новая миграция сразу применяется локально к `tale` (`make migrate`) и `tale_test`; если меняется структура `settings` или семантика колонок, проверить и поправить строки уже существующих игр (миграцией, а для dev-значений — `UPDATE` руками) и перезапустить бота (`make restart`, затем `make logs`; `docs/go_commands.md`).
- Файл миграции `NNNNN_<что>.sql` с `-- +goose Up` / `-- +goose Down`. После коммита миграция неизменяема; исправление — новая миграция.
- Имена: snake_case; таблицы во множественном числе (`games`, `characters`); PK `id`; FK `<сущность>_id`; время `timestamptz`; `created_at`, `updated_at`. Тип PK по ADR-0005.
- Каждая игровая таблица содержит `game_id` с индексом; каждый запрос фильтрует по `game_id`. Запрос без `game_id` к игровой таблице — дефект. Таблицы уровня бота (`users`, `chats`, `chat_moderators`, `telegram_updates`, `llm_profiles`, `app_settings`) без `game_id`.
- Связи внутри БД по `id`; идентификаторы Telegram хранятся только в `users.telegram_id`, `chats.telegram_chat_id`, `messages_outbox.telegram_chat_id` (ADR-0007).
- Идемпотентность: уникальные ключи на внешние события (`telegram_update_id`, `(game_id, round_no)`, `(round_id, character_id)` для заявок); `INSERT ... ON CONFLICT DO NOTHING`.
- Разрешение раунда — одна транзакция; outbox-сообщения пишутся в ней же; воркеры берут работу через `SELECT ... FOR UPDATE SKIP LOCKED`.
- Репозитории: методы с `ctx`, работают через `Querier` из `db(ctx)` (транзакция в контексте после `InTx`, иначе пул; ADR-0008); SQL в константах рядом с методом; ORM нет. Новый метод `Store` дублируется в `adapter/postgres/fake`.
- JSONB для гибких полей (параметры действий, характеристики) с `schema_version`.
- `world_events` только добавляется; состояние мира меняется только через события. Клетки и объекты пакета мира (пункт 27) создаются в транзакции раунда вместе с событиями `cell_generated`, `object_created`; квесты, этапы и цели (`quests`, `quest_stages`, `faction_goals`, пункт 28; `quests.go`) — там же вместе с приватными событиями квестов, знания награды — `SaveCharacterKnowledge`; `generation_jobs` пишется через пул вне транзакции (`SaveGenerationJob`) и без внешних ключей: `FOR UPDATE` на `games` в транзакции раунда заблокировал бы проверку ключа; чистка — `DeleteGameProgress`, `ResetWorld` (удаляет и квесты с целями), `wipe-sandbox.sql`.
- Новая таблица с `game_id` → `DELETE` в `scripts/wipe-sandbox.sql` (порядок по зависимостям; контроль в конце файла падает, если забыть).
