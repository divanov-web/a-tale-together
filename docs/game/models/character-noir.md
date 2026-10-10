# Модель персонажа: нуар (гипотеза)

Код `noir`, черновик второй модели: проверяет, что модель персонажа действительно данные, а не код. Город, детективы, 1940-е. Не входит в этап 1.

| Часть | Содержание |
|---|---|
| Характеристики | `nerve` Хладнокровие, `insight` Интуиция, `charm` Харизма, `grit` Выносливость; 2–4, сумма 12 |
| Навыки | `investigation` расследование (Интуиция), `streetwise` знание улиц (Харизма), `shadowing` слежка (Хладнокровие), `brawl` драка (Выносливость), `fast_talk` заговаривание (Харизма), `shooting` стрельба (Хладнокровие) |
| Архетипы | `detective`, `reporter`, `cop`, `hustler` |
| HP | `3 + grit` |
| Особое | ресурс `heat` (внимание полиции и мафии) 0–5: растёт от шумных действий, снижает шанс скрытных проверок на 1 кубик при `heat ≥ 3` |

Поле `resources` в `definition` — расширение базовой схемы: список именованных счётчиков с диапазоном и эффектами, который движок читает как данные.

```json
{
  "schema_version": 1,
  "attributes": [{"key": "nerve", "title": "Хладнокровие", "min": 2, "max": 4}, {"key": "insight", "title": "Интуиция", "min": 2, "max": 4}, {"key": "charm", "title": "Харизма", "min": 2, "max": 4}, {"key": "grit", "title": "Выносливость", "min": 2, "max": 4}],
  "attribute_points": 12,
  "skills": [{"key": "investigation", "title": "Расследование", "attribute": "insight"}, {"key": "streetwise", "title": "Знание улиц", "attribute": "charm"}, {"key": "shadowing", "title": "Слежка", "attribute": "nerve"}, {"key": "brawl", "title": "Драка", "attribute": "grit"}, {"key": "fast_talk", "title": "Заговаривание", "attribute": "charm"}, {"key": "shooting", "title": "Стрельба", "attribute": "nerve"}],
  "skill_points": 6,
  "skill_max": 3,
  "hp_formula": "3 + grit",
  "inventory_limit": 5,
  "resources": [{"key": "heat", "title": "Внимание", "min": 0, "max": 5, "start": 0, "effects": [{"when": "heat >= 3", "skill_tags": ["stealth"], "dice": -1}]}],
  "archetypes": []
}
```
