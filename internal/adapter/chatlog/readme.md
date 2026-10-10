# adapter/chatlog
- Назначение: файловый журнал чатов для разбора ошибок ИИ ([ADR-0012](../../../docs/decisions/0012-chat-logs.md), [chat-logs.md](../../../docs/dev/chat-logs.md)): `Dir/<telegram_chat_id>/<ГГГГ-ММ-ДД>.jsonl`, запись `model.ChatLogRecord` в строке.
- Публичный вход: `New(Settings{Dir, Retention}, logger)`, `Writer.Write(ctx, rec)` (ошибка только в лог; nil-писатель безопасен), `Writer.Cleanup(now)` (удаляет файлы дней старше срока), `Writer.Dir()`.
- Реализует интерфейс `Journal` сценариев `app/bot`, `app/delivery`, `app/llm`, `app/round`; `cmd/bot` подставляет его при непустом `CHAT_LOG_DIR`.
- Тесты: `writer_test.go` на временной папке.
