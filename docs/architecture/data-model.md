# Модель данных

Тип идентификаторов: `bigint identity` (ADR-0005). Миграции: `migrations/`. Таблицы уровня бота без `game_id`; все игровые таблицы содержат `game_id`.

## Основные сущности

Названы руководителем; вспомогательные сущности Claude заводит сам.

| Сущность | Таблица | Что это | Связи |
|---|---|---|---|
| Пользователь | `users` | уникальный пользователь Telegram, писавший боту или добавивший его в чат | владеет чатами, участвует в играх, имеет персонажей |
| Чат | `chats` | Telegram-группа, куда добавили бота: владелец, модераторы, статус бота в чате | 1 владелец; N модераторов (`chat_moderators`); N игр, из них не более одной незавершённой |
| Игра | `games` | запущенный мир: жанр из справочника, формат, режим, статус, игровое окно, длительность раунда; привязан к чату | 1 чат; 1 жанр; N участников (`game_members`); N сезонов, фракций, групп, раундов, локаций |
| Персонаж | `characters` | персонаж пользователя в сезоне игры: никнейм, характеристики по модели персонажа, открытое и скрытое описание, статус, фракция, группа | ровно один на пару (сезон, пользователь); требует участия в игре; в сериале новый сезон — новая строка с перенесённой прокачкой |
| Фракция | `factions` | сторона конфликта в игре; на старте две (добро и зло), новые создаёт рассказчик по сюжету с именами по сюжету | N персонажей; имена по умолчанию из шаблона жанра ([фракции](../game/factions.md)) |
| Группа | `parties` | логическая группа персонажей, для которой рассказчик пишет общую историю; на старте не более 3 групп в сезоне, размер не ограничен | N персонажей; совместные испытания только внутри группы ([группы](../game/parties.md)) |

Вспомогательные: `chat_moderators` (права в чате), `dialogs` (состояние диалогов в личке), `game_members` (участие и роль в игре), `seasons` (сезоны), `genres` и `character_models` (справочники жанров и моделей персонажа), `character_stats` (значения характеристик и навыков), `character_goals` и `goal_steps` (поздний этап), `telegram_updates` (дедупликация), остальные таблицы раунда и LLM ниже.

```text
genres ──> character_models
  └──< games
users ──< chats (owner_user_id)
  │          └──< chat_moderators >── users
  │          └──< games
  │                 ├──< game_members >── users
  │                 ├──< seasons ──< rounds
  │                 │       └──< characters (season_id, user_id) ── game_members
  │                 │               ├──< character_stats   (ключи из character_models)
  │                 │               ├──> factions, parties
  │                 ├──< locations, action_submissions, resolved_actions,
  │                 │    world_events, messages_outbox, llm_runs
  └──────────────────┘
```

Приватный чат пользователя с ботом отдельной сущностью не является: его `chat_id` равен `telegram_id` пользователя; факт открытия канала хранится в `users.started_at` (ADR-0007).

## Таблицы уровня бота

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `users` | пользователи Telegram | `telegram_id`, `username`, `first_name`, `language_code`, `started_at` (первый `/start` в личке, nullable), `current_game_id` → `games` (игра для команд в личке, ADR-0008), `last_seen_at` | `telegram_id` |
| `chats` | группы с ботом | `telegram_chat_id`, `type` (group/supergroup), `title`, `owner_user_id` → `users`, `bot_status` (member/admin/left), `added_at`, `left_at` | `telegram_chat_id` |
| `chat_moderators` | права в чате, ручной список владельца | `chat_id` → `chats`, `user_id` → `users`, `granted_by_user_id` | `(chat_id, user_id)` |
| `telegram_updates` | дедупликация входящих | `update_id`, `received_at` | `update_id` |
| `dialogs` | состояние диалога в личке (настройка игры, создание персонажа; ADR-0008) | `user_id` → `users`, `kind`, `step`, `game_id`, `data jsonb` (черновик), `updated_at` | `user_id` |
| `llm_profiles` | настройки провайдера LLM (миграция `00009`, стартовый `local-claude`; модели `sonnet` / `opus` с миграции `00011`) | `name`, `provider`, `model_interpreter`, `model_narrator`, `params jsonb` (effort, temperature, max_output, max_budget_usd, timeout_seconds, parallelism), `enabled` | `name` |
| `genres` | справочник жанров с шаблонами | `code`, `name`, `character_model_id` → `character_models`, `template jsonb` (тон, стартовая сцена, архетипы, имена фракций по умолчанию, стартовые локации), `enabled` | `code` |
| `character_models` | модель персонажа ([модели](../game/models/README.md)) | `code`, `rules_version`, `definition jsonb` (attributes[], skills[], archetypes[], стартовые очки, формула HP) | `code` |
| `app_settings` | глобальные настройки бота (миграция `00009`) | `key`, `value` (в т.ч. `default_llm_profile`), `updated_at` | `key` |

## Таблицы игры

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `games` | настройки и статус мира | `chat_id` → `chats`, `genre_id` → `genres`, `format` (global/novel/series, по умолчанию `novel`; этап 1 только он), `mode` (mechanics/balanced/narrative, по умолчанию `balanced`; этап 1 только он), `title`, `status` (draft/active/paused/finished; список расширяется по ходу разработки), `timezone`, `play_from time` и `play_to time` (игровое окно в сутках, оба nullable), `round_minutes` (10–60 в приложении, в dev от 1; проверка базы 1–60, миграция `00014`; по умолчанию 15), `settings jsonb` (ends_at, max_rounds, max_round_players = 20 — лимит обрабатываемых за раунд игроков, внутренняя настройка, миграция `00006`; max_active_locations = 4 — лимит локаций, внутренняя настройка; max_parties = 3, max_active_goals, goal_cooldown_rounds, min_round_answers = 2 — кворум раунда, миграция `00005`; allow_resubmit = false — замена заявки, внутренняя настройка; death_mode = fake и death_days = 10 — режим и срок [смерти персонажа](../game/death.md), пункт 07), `rules_version`, `llm_profile_id` → `llm_profiles` (nullable; миграция `00009`), `join_code`, `created_by_user_id` → `users`, `next_round_at` | `join_code`; частичный `chat_id where status <> 'finished'` |
| `game_members` | участие пользователей | `game_id`, `user_id` → `users`, `role` (admin/player), `status` (active/left/kicked), `joined_at` | `(game_id, user_id)` |
| `factions` | стороны конфликта ([модель](../game/models/factions.md)) | `game_id`, `code` (good, evil или код фракции, созданной рассказчиком), `title`, `description`, `tone`, `relations jsonb` | `(game_id, code)` |
| `parties` | группы персонажей для общей истории ([модель](../game/models/party.md)) | `game_id`, `season_id`, `name`, `status` (active/disbanded), `leader_character_id`, `location_id`, `motto`, `summary` | `(season_id, name)`; число активных групп сезона ≤ `settings.max_parties` |
| `characters` | персонажи | `game_id`, `season_id` → `seasons`, `user_id`, `model_id` → `character_models`, `nickname`, `archetype`, `public_bio varchar(100)`, `secret_bio varchar(100)`, `status` (active/waiting/inactive/dead), `missed_rounds`, `death_rounds_left` и `died_at` (ненастоящая смерть, миграция `00010`), `knowledge jsonb` (факты из `fact_learned`), `faction_id` → `factions`, `party_id` → `parties` (nullable), `hp`, `conditions jsonb`, `inventory jsonb`, `location_id`, `xp` | `(season_id, user_id)`; FK `(game_id, user_id)` → `game_members` |
| `character_stats` | значения характеристик и навыков | `game_id`, `character_id`, `kind` (attribute/skill), `key` (из `character_models.definition`), `value` | `(character_id, kind, key)` |
| `seasons` | сезоны сериала; у остальных типов единственный сезон 1 | `game_id`, `season_no`, `title`, `status` (active/finished), `started_at`, `finished_at` | `(game_id, season_no)` |
| `locations` | места и связи (миграция `00010`; создаются с миром из `genres.template.locations`) | `game_id`, `name`, `description`, `links jsonb` (`to`, `title`, `hidden`), `difficulty` (0–2, прибавляется к сложности проверок), `objects jsonb` ([модель](../game/models/location.md): `key`, `title`, `state`, `revealed`, `approaches`), `active`, `start` (стартовая локация персонажей) | `(game_id, name)` |
| `rounds` | раунды (миграция `00008`) | `game_id`, `season_id`, `round_no`, `opens_at`, `deadline_at`, `closed_at`, `status` (open/frozen/skipped/resolving/resolved/published/failed; после пункта 07 заморозка и разрешение в одной транзакции → `published`), `seed`, `snapshot jsonb` (v2: режим, персонажи с характеристиками и инвентарём, локации с объектами, заявки), `narration jsonb` (ответ рассказчика: `prompt_version`, `chronicle`, `scenes[{location_id, party, text, event_seqs}]`, `patched`; миграция `00012`, пункт 09; пуст при шаблонной хронике), `skip_reason` (причина пропуска или сбоя), `attempts` (откатов закрытия; после 3 — `failed`) и `metrics jsonb` (`model.RoundMetrics`: длительности шагов, вызовы и токены, запасные пути, статусы заявок; миграция `00013`, пункт 10, [ADR-0013](../decisions/0013-round-resilience.md)) | `(game_id, round_no)`; частичный `game_id where status = 'open'` |
| `action_submissions` | заявки (миграция `00008`) | `game_id`, `round_id`, `character_id`, `raw_text`, `status` (pending/frozen; статусы разрешения — пункт 07), `submitted_at`, `updated_at`, `telegram_message_id` | `(round_id, character_id)`; при пропуске раунда `round_id` переносится в следующий |
| `resolved_actions` | намерения и итоги (миграция `00010`) | `game_id`, `round_id`, `submission_id`, `character_id`, `intent jsonb`, `status` (resolved/rejected/deferred/ambiguous), `outcome`, `reason`, `summary`, `checks jsonb` (бросок), `event_seqs`, `conflict_group` | `submission_id` |
| `world_events` | журнал событий (миграция `00010`; только добавляется) | `game_id`, `round_id`, `seq` (сквозной в игре), `type` (`character_moved`, `check_rolled`, `object_changed`, `fact_learned`, `condition_added`, `hp_changed`, `character_waited`, `help_given`, `action_rejected`, `character_died`, `character_returned`), `payload jsonb`, `visibility` (public/location/private), `visible_to bigint[]`, `location_id`, `character_id` | `(game_id, seq)` |
| `messages_outbox` | исходящие (миграция `00008`, ADR-0009) | `game_id`, `round_id` (nullable), `telegram_chat_id`, `kind` (scene/round_open/round_closed/system/chronicle/private — приватное уведомление о заявке шаблоном), `part`, `text`, `status` (pending/sending/sent/failed/unknown), `telegram_message_id`, `attempts`, `send_after`, `claimed_at`, `last_error`, `sent_at` | дедуп по `(round_id, telegram_chat_id, kind, part)` при заданном `round_id` |
| `llm_runs` | прогоны LLM (миграция `00009`) | `game_id` (nullable: `/llm test` вне игры), `round_id` (nullable), `profile_id`, `provider`, `role` (interpreter/narrator/test), `model`, `prompt_version`, `tokens_in`, `tokens_out`, `cost_usd`, `subscription bool`, `duration_ms`, `error`, `request_id` | |

## Таблицы поздних этапов

| Таблица | Назначение | Ключевые поля | Уникальность | Пункт |
|---|---|---|---|---|
| `character_goals` | личные сюжетные цели | `game_id`, `character_id`, `text`, `status` (active/achieved/abandoned), `created_at`, `achieved_at`, `abandoned_at` | лимит активных по `settings.max_active_goals`; новая не раньше `goal_cooldown_rounds` после отказа | [17](../plan/17-story-goals.md) |
| `goal_steps` | шаги цели | `game_id`, `goal_id`, `step_no` (1–10), `text`, `status` (pending/done), `done_round_id`, `stalled_rounds` | `(goal_id, step_no)` | 17 |

## Инварианты

- Внутри БД связи по `id`; идентификаторы Telegram (`telegram_id`, `telegram_chat_id`) только в `users`, `chats`, `messages_outbox`.
- В чате не более одной незавершённой игры; завершённые остаются историей. У игры всегда есть активный сезон; у `global` и `story` он единственный.
- Персонаж создаётся только у активного участника игры; у пары (сезон, пользователь) не более одного персонажа. Ключи `character_stats` только из модели персонажа жанра.
- На старте у игры две фракции, новые создаёт рассказчик по сюжету; персонаж всегда в одной фракции. Активных групп в сезоне не больше `max_parties`; размер группы не ограничен; персонаж не более чем в одной группе сезона. Расхождение участников по локациям создаёт новую группу (`party_split`) только в пределах лимита, иначе движок удерживает группу вместе (`party_held`, [группы](../game/parties.md)).
- `secret_bio` и `public_bio` — текст игрока: в промпт попадают как данные в выделенном блоке, `secret_bio` в контекст рассказчика только как данные для подачи линии персонажа, в хронике не раскрывается.
- Состояние персонажей и локаций производно от `world_events`: сценарий раунда применяет события свёрткой `engine.Apply` и сохраняет результат; прямые правки без события запрещены (исключение — пропуски и счётчик смерти при заморозке, они не события мира).
- У каждой заявки раунда ровно одна строка в `resolved_actions` после разрешения.
- `rounds.snapshot` хранит версию состояния и список заявок на момент дедлайна; воспроизведение движка по снимку и `seed`. У игры не более одного открытого раунда; `games.next_round_at` — единственный таймер планировщика (ADR-0009).
- Повторная отправка outbox не создаёт второе сообщение: `telegram_message_id` сохраняется, неопределённый исход помечается и перепроверяется.

## Открыто

Хранение «знаний персонажа» (отдельная таблица `character_knowledge` или факты в событиях); срок хранения `raw_text` (G8).
