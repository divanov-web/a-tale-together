# Модель данных

Минимальные сущности из концепта §8. Все игровые таблицы содержат `game_id`. Тип идентификаторов: ADR-0005 (ожидает). Миграции: `migrations/`.

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `games` | настройки и статус мира | `chat_id`, `title`, `genre`, `status`, `settings jsonb` (round_duration_minutes, timezone, active_windows, pause_overnight, max_players, max_active_locations), `rules_version`, `llm_profile_id` (nullable), `next_round_at` | `chat_id` |
| `game_members` | участие пользователей | `game_id`, `telegram_user_id`, `role` (admin/player), `private_chat_id`, `joined_at` | `(game_id, telegram_user_id)` |
| `characters` | персонажи | `game_id`, `member_id`, `name`, `archetype`, `stats jsonb`, `hp`, `conditions jsonb`, `inventory jsonb`, `location_id`, `schema_version` | `(game_id, member_id)` |
| `locations` | места и связи | `game_id`, `name`, `description`, `links jsonb`, `active` | `(game_id, name)` |
| `rounds` | раунды | `game_id`, `round_no`, `opens_at`, `deadline_at`, `status` (open/frozen/resolving/resolved/published/failed), `seed`, `snapshot jsonb` | `(game_id, round_no)` |
| `action_submissions` | заявки | `game_id`, `round_id`, `character_id`, `raw_text`, `status`, `submitted_at`, `telegram_message_id` | `(round_id, character_id)` |
| `resolved_actions` | намерения и итоги | `game_id`, `round_id`, `submission_id`, `intent jsonb`, `status`, `checks jsonb`, `conflict_group` | `submission_id` |
| `world_events` | журнал событий | `game_id`, `round_id`, `seq`, `type`, `payload jsonb`, `visibility`, `visible_to bigint[]` | `(game_id, seq)` |
| `messages_outbox` | исходящие | `game_id`, `round_id`, `chat_id`, `text`, `kind` (chronicle/private/system), `status`, `telegram_message_id`, `attempts`, `send_after` | дедуп по `(round_id, chat_id, kind, part)` |
| `telegram_updates` | дедупликация входящих | `update_id`, `received_at` | `update_id` |
| `llm_profiles` | настройки провайдера LLM | `name`, `provider`, `model_interpreter`, `model_narrator`, `params jsonb`, `enabled` | `name` |
| `app_settings` | глобальные настройки бота | `key`, `value` (в т.ч. `default_llm_profile`) | `key` |
| `llm_runs` | прогоны LLM | `game_id`, `round_id`, `profile_id`, `provider`, `role` (interpreter/narrator), `model`, `prompt_version`, `tokens_in`, `tokens_out`, `cost_usd`, `subscription bool`, `duration_ms`, `error`, `request_id` | |

## Инварианты

- Состояние персонажей и локаций производно от `world_events`; прямые правки без события запрещены.
- У каждой заявки раунда ровно одна строка в `resolved_actions` после разрешения.
- `rounds.snapshot` хранит версию состояния и список заявок на момент дедлайна; воспроизведение движка по снимку и `seed`.
- Повторная отправка outbox не создаёт второе сообщение: `telegram_message_id` сохраняется, неопределённый исход помечается и перепроверяется.

## Открыто

Тип PK (ADR-0005); хранение «знаний персонажа» (отдельная таблица `character_knowledge` или факты в событиях); срок хранения `raw_text` (концепт §14 п. 8).
