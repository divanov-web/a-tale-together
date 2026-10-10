# Модель персонажа: базовая (фэнтези)

Код `basic_fantasy`, `rules_version` 0. Используется жанром [фэнтези](genre.md). Поведение: [персонаж](../character.md), [испытания](../checks.md).

## Состав
| Часть | Содержание |
|---|---|
| Характеристики | `strength` Сила, `agility` Ловкость, `wits` Разум, `will` Воля; 2–4 на старте, сумма 12 |
| Навыки | `thievery` воровство (Ловкость), `awareness` внимательность (Разум), `persuasion` убеждение (Воля), `survival` выживание (Разум), `stealth` скрытность (Ловкость), `melee` бой (Сила); 0–3, на старте 6 очков, не более 3 в один |
| Архетипы | `warrior`, `rogue`, `mage`, `diplomat`, `ranger`: рекомендуемое распределение и стартовый инвентарь; используются автораспределением |
| HP | `4 + strength` |
| Инвентарь | до 6 предметов |

## Пример `definition`
```json
{
  "schema_version": 1,
  "attributes": [
    {"key": "strength", "title": "Сила", "min": 2, "max": 4},
    {"key": "agility", "title": "Ловкость", "min": 2, "max": 4},
    {"key": "wits", "title": "Разум", "min": 2, "max": 4},
    {"key": "will", "title": "Воля", "min": 2, "max": 4}
  ],
  "attribute_points": 12,
  "skills": [
    {"key": "thievery", "title": "Воровство", "attribute": "agility"},
    {"key": "awareness", "title": "Внимательность", "attribute": "wits"},
    {"key": "persuasion", "title": "Убеждение", "attribute": "will"},
    {"key": "survival", "title": "Выживание", "attribute": "wits"},
    {"key": "stealth", "title": "Скрытность", "attribute": "agility"},
    {"key": "melee", "title": "Бой", "attribute": "strength"}
  ],
  "skill_points": 6,
  "skill_max": 3,
  "hp_formula": "4 + strength",
  "inventory_limit": 6,
  "archetypes": [
    {"key": "warrior", "title": "Воин", "attributes": {"strength": 4, "agility": 3, "wits": 2, "will": 3}, "skills": {"melee": 3, "survival": 2, "awareness": 1}, "items": ["sword", "shield"]},
    {"key": "rogue", "title": "Плут", "attributes": {"strength": 2, "agility": 4, "wits": 3, "will": 3}, "skills": {"thievery": 3, "stealth": 2, "awareness": 1}, "items": ["dagger", "lockpicks"]},
    {"key": "mage", "title": "Маг", "attributes": {"strength": 2, "agility": 2, "wits": 4, "will": 4}, "skills": {"awareness": 3, "persuasion": 2, "survival": 1}, "items": ["staff", "tome"]},
    {"key": "diplomat", "title": "Дипломат", "attributes": {"strength": 2, "agility": 3, "wits": 3, "will": 4}, "skills": {"persuasion": 3, "awareness": 2, "thievery": 1}, "items": ["seal", "coin_purse"]},
    {"key": "ranger", "title": "Следопыт", "attributes": {"strength": 3, "agility": 4, "wits": 3, "will": 2}, "skills": {"survival": 3, "stealth": 2, "melee": 1}, "items": ["bow", "rope"]}
  ]
}
```

## Валидация
Сумма характеристик и очков навыков равна лимитам модели; ключи `character_stats` только из модели; архетип из списка; предметы — записи каталога предметов жанра.
