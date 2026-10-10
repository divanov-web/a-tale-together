# app/bot
- Назначение: сценарий входящих Telegram: дедупликация по `update_id`, лимит частоты (`ratelimit.go`), учёт пользователя, реестр команд с контекстами и подсказкой в чужом контексте (`router.go`), регистрация чата по `my_chat_member`, диалоги в личке (`dialog*.go`: настройка игры, создание персонажа; состояние в таблице `dialogs`), ответы шаблонами `templates/`.
- Публичный вход: `New(Settings, Store, Dialogs, Telegram, *chat.Service, *game.Service, *round.Service, *llm.Service, Clock, logger)` (`Settings.OwnerID` — владелец бота для `/llm`), `Service.Handle`, `Service.HandleChatMember`, `Service.RegisterMenu`, `Service.Commands`.
- Интерфейсы потребителя: `Store`, `Dialogs` (`adapter/postgres`), `Telegram` (`adapter/telegram`), `Clock`.
- Файлы: `cmd_basic.go` (`/start /help /me /ping /cancel`), `cmd_chat.go` (`/chat /mod /owner`), `cmd_game.go` (`/create /status /set /join /games`), `cmd_character.go` (`/character /create /bio /secret`), `cmd_round.go` (`/begin /pause /act`, `/next` и `/replay` только при `Env == dev`; строки о раундах в `/status`), `cmd_llm.go` (`/llm` с подкомандами `use reset add test`, только владелец бота).
- Ошибки сценариев из списка `userErrors` уходят игроку текстом; остальные — «Что-то пошло не так» и возврат ошибки в лог.
- Правило: обработчики тонкие, игровой логики и SQL нет; ответы на команды синхронные (ADR-0006), игровые тексты через outbox. Реестр: [telegram-commands.md](../../../docs/architecture/telegram-commands.md).
- Тесты: сквозные сценарии на `adapter/postgres/fake` (`service_test.go`).
