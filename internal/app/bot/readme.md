# app/bot
- Назначение: сценарий входящих сообщений Telegram: дедупликация по `update_id`, учёт пользователя, маршрутизация команд к сценариям игры, ответы шаблонами.
- Публичный вход: `New(Settings, Store, Sender, Clock, logger)`, `Service.Handle(ctx, model.Incoming)`.
- Интерфейсы потребителя: `Store` (`adapter/postgres`), `Sender` (`adapter/telegram`), `Clock`.
- Правило: обработчики тонкие, игровой логики и SQL нет; ответы на команды синхронные (ADR-0006), игровые тексты через outbox.
- Команды: `/ping`. Реестр: [telegram-commands.md](../../../docs/architecture/telegram-commands.md).
