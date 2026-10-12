# Модель локации и объектов

Поведение: [испытания](../checks.md) (предлагаемые действия, сложность локации). Пункт [27](../../plan/telegram/27-world-grid.md) превращает локацию в клетку сетки: координаты, стадия, выходы по направлениям, объекты в таблице `world_objects` ([динамический мир](../world.md)); поля ниже — текущая реализация.

## Поля локации
| Поле | Тип | Назначение |
|---|---|---|
| `name` | строка, уникально в игре | |
| `description` | ≤ 300 | для рассказчика и игроков |
| `difficulty` | 0–2 | прибавляется к сложности всех проверок в локации |
| `links` | список `{to, title, hidden}` | выходы; `hidden` открывается событием |
| `objects` | список объектов | с чем можно взаимодействовать |
| `active` | bool | входит ли в лимит активных локаций |
| `start` | bool | стартовая локация новых персонажей (одна на игру; без флага — первая в шаблоне) |

## Объект
| Поле | Назначение |
|---|---|
| `key`, `title` | идентификатор для интерпретатора, имя для игрока |
| `state` | `locked / open / broken / hidden …`; меняется событиями |
| `approaches` | предлагаемые подходы: `action`, `title`, `attribute`, `skill`, `difficulty` (0 и без характеристики — без броска), `on_success` (`{"state": …}` для объекта или `{"object": key, "state": …}`, `{"reveal": key}`, `{"fact": текст}`), `on_fail` (`{"condition": …}`, `{"damage": N}`), `deadly` (провал не заменяется успехом с ценой в повествовании) |
| `hidden_until_revealed` | объект не виден и не доступен до события `reveal` (поле `revealed`) |
| `visible_when` | условие видимости по состоянию другого объекта или знанию персонажа — поздний этап |

```json
{
  "name": "Подвал башни",
  "description": "Сырой подвал, запертая дверь в глубине и следы сапог на пыли.",
  "difficulty": 1,
  "links": [{"to": "tower_hall", "title": "наверх в зал", "hidden": false}],
  "objects": [
    {
      "key": "cellar_door", "title": "запертая дверь", "state": "locked",
      "approaches": [
        {"action": "interact", "attribute": "strength", "skill": "athletics", "difficulty": 4, "on_success": {"state": "broken"}, "on_fail": {"condition": "exhausted"}},
        {"action": "interact", "attribute": "agility", "skill": "thievery", "difficulty": 4, "on_success": {"state": "open"}},
        {"action": "explore", "attribute": "wits", "skill": "awareness", "difficulty": 4, "on_success": {"reveal": "hidden_lever"}}
      ]
    },
    {"key": "hidden_lever", "title": "потайной рычаг", "state": "hidden", "visible_when": {"revealed": true}, "approaches": [{"action": "interact", "attribute": null, "skill": null, "difficulty": 0, "on_success": {"object": "cellar_door", "state": "open"}}]}
  ]
}
```

Сложность в `approaches` — базовая; движок прибавляет `difficulty` локации, показывает в группе итог, а игроку — его шанс по параметру ([испытания](../checks.md)). Обычный подход — 5 итого: в локации с надбавкой 1 база 4.
