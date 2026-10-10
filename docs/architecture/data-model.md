# Модель данных

Тип идентификаторов: `bigint identity` (ADR-0005). Миграции: `migrations/`. Таблицы уровня бота без `game_id`; все игровые таблицы содержат `game_id`.

## Основные сущности

Названы руководителем; вспомогательные сущности Claude заводит сам.

| Сущность | Таблица | Что это | Связи |
|---|---|---|---|
| Пользователь | `users` | уникальный пользователь Telegram, писавший боту или добавивший его в чат | владеет чатами, участвует в играх, имеет персонажей |
| Чат | `chats` | Telegram-группа, куда добавили бота: владелец, модераторы, статус бота в чате | 1 владелец; N модераторов (`chat_moderators`); N игр, из них не более одной незавершённой |
| Игра | `games` | запущенный мир с настройками и типом (глобальная, рассказ, сериал), привязан к чату | 1 чат; N участников (`game_members`); N персонажей, сезонов, раундов, локаций |
| Персонаж | `characters` | персонаж пользователя в конкретной игре | ровно один на пару (игра, пользователь); требует участия в игре |

Вспомогательные: `chat_moderators` (права в чате), `game_members` (участие и роль в игре), `seasons` (сезоны сериала), `character_goals` и `goal_steps` (поздний этап), `telegram_updates` (дедупликация), остальные таблицы раунда и LLM ниже.

```text
users ──< chats (owner_user_id)
  │          └──< chat_moderators >── users
  │          └──< games
  │                 ├──< game_members >── users
  │                 ├──< characters  (game_id, user_id) ── game_members
  │                 ├──< seasons ──< rounds
  │                 ├──< locations, action_submissions, resolved_actions,
  │                 │    world_events, messages_outbox, llm_runs
  └──────────────────┘
```

Приватный чат пользователя с ботом отдельной сущностью не является: его `chat_id` равен `telegram_id` пользователя; факт открытия канала хранится в `users.started_at` (ADR-0007).

## Таблицы уровня бота

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `users` | пользователи Telegram | `telegram_id`, `username`, `first_name`, `language_code`, `started_at` (первый `/start` в личке, nullable), `last_seen_at` | `telegram_id` |
| `chats` | группы с ботом | `telegram_chat_id`, `type` (group/supergroup), `title`, `owner_user_id` → `users`, `bot_status` (member/admin/left), `added_at`, `left_at` | `telegram_chat_id` |
| `chat_moderators` | права в чате, ручной список владельца | `chat_id` → `chats`, `user_id` → `users`, `granted_by_user_id` | `(chat_id, user_id)` |
| `telegram_updates` | дедупликация входящих | `update_id`, `received_at` | `update_id` |
| `llm_profiles` | настройки провайдера LLM | `name`, `provider`, `model_interpreter`, `model_narrator`, `params jsonb`, `enabled` | `name` |
| `app_settings` | глобальные настройки бота | `key`, `value` (в т.ч. `default_llm_profile`) | `key` |

## Таблицы игры

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `games` | настройки и статус мира | `chat_id` → `chats`, `kind` (global/story/serial, [типы игр](../game/game-types.md)), `title`, `genre`, `status` (draft/active/paused/finished), `settings jsonb` (ends_at, max_rounds, round_duration_minutes, timezone, active_windows, pause_overnight, max_players, max_active_locations, story_mode mechanics/balanced/story), `rules_version`, `llm_profile_id` (nullable), `join_code`, `next_round_at` | `join_code`; частичный `chat_id where status <> 'finished'` |
| `game_members` | участие пользователей | `game_id`, `user_id` → `users`, `role` (admin/player), `status` (active/left/kicked), `joined_at` | `(game_id, user_id)` |
| `characters` | персонажи | `game_id`, `user_id`, `name`, `archetype`, `stats jsonb`, `hp`, `conditions jsonb`, `inventory jsonb`, `location_id`, `schema_version` | `(game_id, user_id)`; FK `(game_id, user_id)` → `game_members` |
| `seasons` | сезоны сериала; у остальных типов единственный сезон 1 | `game_id`, `season_no`, `title`, `status` (active/finished), `started_at`, `finished_at` | `(game_id, season_no)` |
| `locations` | места и связи | `game_id`, `name`, `description`, `links jsonb`, `active` | `(game_id, name)` |
| `rounds` | раунды | `game_id`, `season_id`, `round_no`, `opens_at`, `deadline_at`, `status` (open/frozen/resolving/resolved/published/failed), `seed`, `snapshot jsonb` | `(game_id, round_no)` |
| `action_submissions` | заявки | `game_id`, `round_id`, `character_id`, `raw_text`, `status`, `submitted_at`, `telegram_message_id` | `(round_id, character_id)` |
| `resolved_actions` | намерения и итоги | `game_id`, `round_id`, `submission_id`, `intent jsonb`, `status`, `checks jsonb`, `conflict_group` | `submission_id` |
| `world_events` | журнал событий | `game_id`, `round_id`, `seq`, `type`, `payload jsonb`, `visibility`, `visible_to bigint[]` | `(game_id, seq)` |
| `messages_outbox` | исходящие | `game_id`, `round_id`, `telegram_chat_id`, `text`, `kind` (chronicle/private/system), `status`, `telegram_message_id`, `attempts`, `send_after` | дедуп по `(round_id, telegram_chat_id, kind, part)` |
| `llm_runs` | прогоны LLM | `game_id`, `round_id`, `profile_id`, `provider`, `role` (interpreter/narrator), `model`, `prompt_version`, `tokens_in`, `tokens_out`, `cost_usd`, `subscription bool`, `duration_ms`, `error`, `request_id` | |

## Таблицы поздних этапов

| Таблица | Назначение | Ключевые поля | Уникальность | Пункт |
|---|---|---|---|---|
| `character_goals` | личные сюжетные цели | `game_id`, `character_id`, `text`, `status` (active/achieved/abandoned), `created_at`, `achieved_at` | лимит активных по `settings.max_active_goals` | [17](../plan/17-story-goals.md) |
| `goal_steps` | шаги цели | `game_id`, `goal_id`, `step_no` (1–10), `text`, `status` (pending/done), `done_round_id` | `(goal_id, step_no)` | 17 |

## Инварианты

- Внутри БД связи по `id`; идентификаторы Telegram (`telegram_id`, `telegram_chat_id`) только в `users`, `chats`, `messages_outbox`.
- В чате не более одной незавершённой игры; завершённые остаются историей. У игры всегда есть активный сезон; у `global` и `story` он единственный.
- Персонаж создаётся только у активного участника игры; у пары (игра, пользователь) не более одного персонажа.
- Состояние персонажей и локаций производно от `world_events`; прямые правки без события запрещены.
- У каждой заявки раунда ровно одна строка в `resolved_actions` после разрешения.
- `rounds.snapshot` хранит версию состояния и список заявок на момент дедлайна; воспроизведение движка по снимку и `seed`.
- Повторная отправка outbox не создаёт второе сообщение: `telegram_message_id` сохраняется, неопределённый исход помечается и перепроверяется.

## Открыто

Хранение «знаний персонажа» (отдельная таблица `character_knowledge` или факты в событиях); срок хранения `raw_text` (концепт §14 п. 8).
