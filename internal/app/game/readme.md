# app/game
- Назначение: сценарии игры: создание мира в чате (игра `draft`, сезон 1, фракции из шаблона жанра, админ), параметры `/set` и диалога настройки (`settings.go`), вход по коду, текущая игра пользователя, сводка `/status`, персонаж (`character.go`: модель жанра, валидация никнейма и описаний, создание с HP и стартовым инвентарём, описания).
- Публичный вход: `New(Store, Rights, Clock, logger)`; поле `Defaults` — настройки нового мира (`cmd` ставит кворум 1 в dev); `Create`, `Set`, `Params`, `Join`, `Current`, `Of`, `Select`, `OfChat`, `ByCode`, `Get`, `CanAdmin`, `Status`, `Factions`, `Genre`, `CharacterModel`, `Character`, `CreateCharacter`, `ValidateNickname`, `ValidateBio`, `SetBio`.
- Интерфейсы потребителя: `Store` (`adapter/postgres`, транзакция через контекст по ADR-0008), `Rights` (`app/chat`), `Clock`.
- Ошибки для ответа игроку: `ErrForbidden`, `ErrGameExists`, `ErrNoGame`, `ErrGameFinished`, `ErrNotMember`, `ErrHasCharacter`, `ErrNoCharacter`, `ErrNicknameTaken`, `ErrChooseGame`, `ErrBadValue`.
- Правило: границы транзакций здесь; правила персонажа в `service/rules`; SQL и Telegram не используются.
- Тесты: сценарии на фейковом хранилище `adapter/postgres/fake`.
