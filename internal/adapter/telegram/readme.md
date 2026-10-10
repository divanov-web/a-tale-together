# adapter/telegram
- Назначение: Telegram Bot API через `go-telegram/bot` (ADR-0004): long polling, разбор команд, отправка текста.
- Публичный вход: `New(ctx, Settings, logger)`, `Client.Run(ctx, Handler)`, `Client.SendText`, `Client.Username`.
- Входящие: только текстовые сообщения → `model.Incoming` с командой без `@bot`; чужие `/cmd@other_bot` отбрасываются.
- Ошибки библиотеки в лог; 409 Conflict означает второй экземпляр с тем же токеном.
- Планируется: деление длинных сообщений и outbox (пункт 09), webhook по отдельному ADR.
- Связи: `.claude/rules/telegram.md`, [команды](../../../docs/architecture/telegram-commands.md).
