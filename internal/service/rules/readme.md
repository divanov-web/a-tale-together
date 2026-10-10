# service/rules
- Назначение: правила игры как данные. Сейчас: модель персонажа (`character.go`): валидация характеристик и навыков по `model.CharacterModelDef`, автораспределение по архетипу, формула HP вида `N + attribute`, разбор ответов игрока (числа по порядку или пары «название значение»), поиск архетипа.
- Публичный вход: `ValidateAttributes`, `ValidateSkills`, `ValidateDraft`, `AutoDistribute`, `HP`, `ParseAttributes`, `ParseSkills`, `FindArchetype`, `ErrInvalid` (текст пригоден для ответа игроку).
- Правило: чистые функции без I/O; ключи только из модели; описание модели в [docs/game/models](../../../docs/game/models/README.md).
- Позже: каталог действий, таблицы сложности и шансов ([испытания](../../../docs/game/checks.md)).
