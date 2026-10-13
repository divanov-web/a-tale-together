---
paths:
  - "migrations/**"
  - "internal/adapter/postgres/**"
  - "**/*.sql"
---
# PostgreSQL

Источники: схема `docs/architecture/data-model.md`, readme `adapter/postgres` и `adapter/postgres/fake`.

- Драйвер `pgx/v5` (`pgxpool`); миграции `goose/v3`, SQL в `migrations/`, встроены через `embed`, применяются `make migrate` или при старте по флагу конфига.
- Файл миграции `NNNNN_<что>.sql` с `-- +goose Up` / `-- +goose Down`. После коммита миграция неизменяема; исправление — новая миграция.
- Новая миграция сразу применяется к `tale` и `tale_test`; изменение `settings` или семантики колонок → проверить строки уже идущих игр (миграцией, dev-значения — `UPDATE` руками) и перезапустить бота (шаг 5 цикла задачи).
- Имена: snake_case; таблицы во множественном числе; PK `id`; FK `<сущность>_id`; время `timestamptz`; `created_at`, `updated_at`. Тип PK по ADR-0005.
- Каждая игровая таблица содержит `game_id` с индексом; запрос без `game_id` к игровой таблице — дефект. Без `game_id` только таблицы уровня бота (`users`, `chats`, `chat_moderators`, `telegram_updates`, `llm_profiles`, `app_settings`).
- Связи внутри БД по `id`; идентификаторы Telegram только в `users.telegram_id`, `chats.telegram_chat_id`, `messages_outbox.telegram_chat_id` (ADR-0007).
- Идемпотентность: уникальные ключи на внешние события (`telegram_update_id`, `(game_id, round_no)`, `(round_id, character_id)` для заявок); `INSERT ... ON CONFLICT DO NOTHING`.
- Разрешение раунда — одна транзакция, outbox и события пакета мира и квестов пишутся в ней же; воркеры берут работу через `SELECT ... FOR UPDATE SKIP LOCKED`.
- Репозитории: методы с `ctx` через `Querier` из `db(ctx)` (транзакция из контекста после `InTx`, иначе пул; ADR-0008); SQL в константах рядом с методом; ORM нет. Новый метод `Store` дублируется в `adapter/postgres/fake`.
- JSONB для гибких полей с `schema_version`.
- `world_events` только добавляется; состояние мира меняется только событиями.
- Ловушка: `generation_jobs` пишется через пул вне транзакции и без внешних ключей — `FOR UPDATE` на `games` в транзакции раунда заблокировал бы проверку ключа. Другая запись «мимо транзакции» — только с таким же обоснованием в readme.
- Новая таблица с `game_id` → `DELETE` в `scripts/wipe-sandbox.sql` в порядке зависимостей (контроль в конце файла падает, если забыть) и чистка в `DeleteGameProgress` / `ResetWorld`.
