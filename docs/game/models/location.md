# Модель локации и объектов

Поведение: [испытания](../checks.md) (предлагаемые действия, сложность локации), лимит активных локаций — настройка игры.

## Поля локации
| Поле | Тип | Назначение |
|---|---|---|
| `name` | строка, уникально в игре | |
| `description` | ≤ 300 | для рассказчика и игроков |
| `difficulty` | 0–2 | прибавляется к сложности всех проверок в локации |
| `links` | список `{to, title, hidden}` | выходы; `hidden` открывается событием |
| `objects` | список объектов | с чем можно взаимодействовать |
| `active` | bool | входит ли в лимит активных локаций |

## Объект
| Поле | Назначение |
|---|---|
| `key`, `title` | идентификатор для интерпретатора, имя для игрока |
| `state` | `locked / open / broken / hidden …`; меняется событиями |
| `approaches` | предлагаемые подходы: `action`, `attribute`, `skill`, `difficulty`, `on_success` (событие), `on_fail` (цена) |
| `visible_when` | условие видимости объекта (состояние другого объекта, знание персонажа) |

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
        {"action": "interact", "attribute": "strength", "skill": null, "difficulty": 2, "on_success": {"state": "broken"}, "on_fail": {"condition": "exhausted"}},
        {"action": "interact", "attribute": "agility", "skill": "thievery", "difficulty": 1, "on_success": {"state": "open"}},
        {"action": "explore", "attribute": "wits", "skill": "awareness", "difficulty": 3, "on_success": {"reveal": "hidden_lever"}}
      ]
    },
    {"key": "hidden_lever", "title": "потайной рычаг", "state": "hidden", "visible_when": {"revealed": true}, "approaches": [{"action": "interact", "attribute": null, "skill": null, "difficulty": 0, "on_success": {"object": "cellar_door", "state": "open"}}]}
  ]
}
```

Сложность в `approaches` — базовая; движок прибавляет `difficulty` локации и показывает игроку итог и шанс.
