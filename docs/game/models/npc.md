# Модель NPC (поздний этап)

Пункт плана 13. NPC — персонаж мира без игрока: состояние и отношение хранит код, реплики пишет рассказчик.

| Поле | Назначение |
|---|---|
| `key`, `title` | идентификатор и имя |
| `faction_code` | фракция или `null` |
| `location_id` | где находится |
| `attitude` | карта `character_id → −2…+2` и по фракциям; меняется событиями `persuade` |
| `knowledge` | факты, которые NPC может выдать, с условиями (`attitude ≥ 1`, предмет, фракция) |
| `stats` | минимальный набор по модели персонажа жанра для встречных проверок |
| `timers` | что NPC делает без игроков: `{"every_rounds": 3, "action": "move", "to": "market"}` |

```json
{"key": "old_jorg", "title": "старый Йорг", "faction_code": "neutral", "location_id": 2, "attitude": {"default": 0}, "knowledge": [{"fact": "гонец ушёл к башне", "when": {"attitude_min": 1}}], "stats": {"wits": 3, "persuasion": 2}, "timers": []}
```
