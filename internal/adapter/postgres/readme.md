# adapter/postgres
- Назначение: хранилище на `pgx/v5` (`pgxpool`): подключение, встроенные миграции goose, репозитории по сущностям (`updates.go`, `users.go`).
- Публичный вход: `New(ctx, Settings, logger)`, `Store.Migrate`, `Store.Close`, методы репозиториев; транзакции `InTx` появятся с первым сценарием раунда.
- Правило: SQL в константах рядом с методом, через `Querier`; игровые таблицы только с `game_id`.
- Тесты: интеграционные с тегом `integration` на `DATABASE_URL_TEST` (`make test-integration`).
- Связи: `migrations/`, `.claude/rules/postgres.md`, [модель данных](../../../docs/architecture/data-model.md).
