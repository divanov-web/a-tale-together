# adapter/telegram
- Назначение: Telegram Bot API через `go-telegram/bot` (ADR-0004): long polling (`message`, `my_chat_member`), разбор команд, отправка текста, меню команд, проверка админа группы.
- Публичный вход: `New(ctx, Settings, logger)`, `Client.Run(ctx, Handler)`, `Client.SendText` (ответы на команды), `Client.Send` (возвращает `message_id`, для воркера outbox), `Client.SetCommands` (меню личек и групп), `Client.SetMemberCommands` (меню участника чата областью `chat_member`, пустой список снимает; B20), `Client.IsChatAdmin`, `Client.Username`.
- Входящие: текстовые сообщения → `model.Incoming` с командой без `@bot` (чужие `/cmd@other_bot` отбрасываются); `my_chat_member` → `model.ChatMemberChange` со статусом бота `member/admin/left`.
- Ошибки библиотеки в лог; 409 Conflict означает второй экземпляр с тем же токеном. `Send` классифицирует ошибки для воркера outbox (ADR-0013, `classify`): `ErrorForbidden`, `ErrorBadRequest`, `ErrorNotFound`, `ErrorUnauthorized`, переезд чата → `model.ErrSendRejected`; `TooManyRequestsError` → `model.RetryAfterError` с `retry_after` (без него минута); остальное — временная ошибка.
- Тесты: `command_test.go` (разбор команд), `client_test.go` (классификация ошибок).
- Длинные игровые тексты делятся на части сценарием при постановке в outbox (`app/round.splitParts`, ADR-0011), адаптер отправляет части как отдельные сообщения. Планируется: webhook по отдельному ADR.
- Связи: `.claude/rules/telegram.md`, [команды](../../../docs/architecture/telegram-commands.md).
