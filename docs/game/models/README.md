# Модели

Модель — описание игровой сущности как данных: поля, ограничения, значения по умолчанию, пример JSON и место хранения. Модели гипотетические (черновик): уточняются сценариями и первыми партиями. Правила поведения — в [правилах](../rules.md); здесь — форма данных.

| Модель | Файл | Где хранится |
|---|---|---|
| Персонаж, базовая (фэнтези) | [character-basic.md](character-basic.md) | `character_models.definition`, значения в `character_stats` |
| Персонаж, нуар (гипотеза второй модели) | [character-noir.md](character-noir.md) | то же |
| Фракции | [factions.md](factions.md) | `genres.template.factions` по умолчанию → строки `factions` игры |
| Группа | [party.md](party.md) | `parties`, `characters.party_id`, `settings.max_parties` |
| Жанр | [genre.md](genre.md) | `genres.template` |
| Локация и объекты | [location.md](location.md) | `locations`, `locations.objects` |
| NPC | [npc.md](npc.md) | `npcs` (пункт 13), поздний этап |

Правила моделей: ключи в JSON на английском в snake_case; русские названия для игрока в поле `title`; каждая модель несёт `schema_version`; движок валидирует данные по модели, LLM их не меняет.
