# app/chat
- Назначение: чаты с ботом (ADR-0007): регистрация по `my_chat_member` и по первой команде администратора группы, владелец, ручной список модераторов, проверка прав на управление играми.
- Публичный вход: `New(Store, logger)`; `BotStatusChanged`, `Ensure`, `CanManage`, `SetOwner`, `AddModerator`, `RemoveModerator`, `Info`.
- Ошибки для ответа в чат: `ErrForbidden`, `ErrUnknown`.
- Связи: [модель данных](../../../docs/architecture/data-model.md), `.claude/rules/telegram.md`.
