# Модели

Модель — описание игровой сущности как данных: поля, ограничения, значения по умолчанию, пример JSON и место хранения. Модели гипотетические (черновик): уточняются сценариями и первыми партиями. Правила поведения — в [правилах](../rules.md); здесь — форма данных.

| Модель | Файл | Где хранится |
|---|---|---|
| Персонаж, базовая (фэнтези) | [character-basic.md](character-basic.md) | `character_models.definition`, значения в `character_stats` |
| Персонаж, нуар (гипотеза второй модели) | [character-noir.md](character-noir.md) | то же |
| Фракции | [factions.md](factions.md) | `genres.template.factions` по умолчанию → строки `factions` игры |
| Группа | [party.md](party.md) | `parties`, `characters.party_id`, `settings.max_parties` |
| Жанр | [genre.md](genre.md) | `genres.template` |
| Клетка и объекты | [location.md](location.md) | `locations` (сетка `x`, `y`, стадия), `world_objects` |
| NPC | [npc.md](npc.md) | `npcs` (таблица с пункта 27, поведение — пункт 30) |
| Квест, этап, цель фракции | [quest.md](quest.md) | `quests`, `quest_stages`, `faction_goals`, `settings.max_story_quests` / `max_side_quests`, шаблон `genres.template.story_quest` |

Правила моделей: ключи в JSON на английском в snake_case; русские названия для игрока в поле `title`; каждая модель несёт `schema_version`; движок валидирует данные по модели, LLM их не меняет.
