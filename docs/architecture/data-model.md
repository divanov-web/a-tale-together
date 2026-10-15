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
| Группа | `parties` | логическая группа персонажей, для которой рассказчик пишет общую историю; на старте не более 4 групп в сезоне, размер не ограничен | N персонажей; совместные испытания только внутри группы ([группы](../game/parties.md)) |

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
  │                 │               ├──> factions ──< faction_goals, quests ──< quest_stages
  │                 │               ├──> parties
  │                 ├──< locations ──< world_objects, location_discoveries
  │                 ├──< items, npcs, generation_jobs
  │                 ├──< action_submissions, resolved_actions,
  │                 │    world_events, messages_outbox, llm_runs
  └──────────────────┘
```

Приватный чат пользователя с ботом отдельной сущностью не является: его `chat_id` равен `telegram_id` пользователя; факт открытия канала хранится в `users.started_at` (ADR-0007).

## Таблицы уровня бота

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `users` | пользователи Telegram | `telegram_id`, `username`, `first_name`, `language_code`, `started_at` (первый `/start` в личке, nullable), `current_game_id` → `games` (игра для команд в личке, ADR-0008), `last_seen_at` | `telegram_id` |
| `chats` | группы с ботом | `telegram_chat_id`, `type` (group/supergroup), `title`, `owner_user_id` → `users`, `bot_status` (member/admin/left), `added_at`, `left_at`, `invite_link` (ссылка-приглашение бота, пусто — нет; сбрасывается при `my_chat_member`, миграция `00026`, ADR-0022) | `telegram_chat_id` |
| `chat_moderators` | права в чате, ручной список владельца | `chat_id` → `chats`, `user_id` → `users`, `granted_by_user_id` | `(chat_id, user_id)` |
| `telegram_updates` | дедупликация входящих | `update_id`, `received_at` | `update_id` |
| `dialogs` | состояние диалога в личке (настройка игры, создание персонажа; ADR-0008) | `user_id` → `users`, `kind`, `step`, `game_id`, `data jsonb` (черновик), `updated_at` | `user_id` |
| `llm_profiles` | настройки провайдера LLM (миграция `00009`, стартовый `local-claude`; модели `sonnet` / `opus` с миграции `00011`) | `name`, `provider`, `model_interpreter`, `model_narrator`, `params jsonb` (effort, temperature, max_output, max_budget_usd, timeout_seconds, parallelism), `enabled` | `name` |
| `genres` | справочник жанров с шаблонами | `code`, `name`, `character_model_id` → `character_models`, `template jsonb` (тон, стартовая сцена, архетипы, имена фракций по умолчанию, стартовая клетка), `enabled` | `code` |
| `character_models` | модель персонажа ([модели](../game/models/README.md)) | `code`, `rules_version`, `definition jsonb` (attributes[], skills[], archetypes[], стартовые очки, формула HP) | `code` |
| `app_settings` | глобальные настройки бота (миграция `00009`) | `key`, `value` (в т.ч. `default_llm_profile`), `updated_at` | `key` |

## Таблицы игры

| Таблица | Назначение | Ключевые поля | Уникальность |
|---|---|---|---|
| `games` | настройки и статус мира | `chat_id` → `chats`, `genre_id` → `genres`, `format` (global/novel/series, по умолчанию `novel`; этап 1 только он), `mode` (mechanics/balanced/narrative, по умолчанию `balanced`; этап 1 только он), `title`, `status` (setup/draft/starting/active/paused/finishing/finished, `CHECK` миграциями `00027`, `00028`; `setup` — до завершения настройки, персонажи не создаются, ADR-0023; `starting` и `finishing` — старт и завершение ждут тика планировщика, ADR-0024), `timezone`, `play_from time` и `play_to time` (игровое окно в сутках, оба nullable), `round_minutes` (5–60 в приложении, на проде минимум поднять до 10; в dev от 2; проверка базы 1–60, миграция `00014`; по умолчанию 15), `settings jsonb` (ends_at, max_rounds, max_round_players = 20 — лимит обрабатываемых за раунд игроков, внутренняя настройка, миграция `00006`; max_parties = 4 — лимит групп и занятых локаций (3 → 4 миграцией `00021`, решение руководителя 2026-10-11) (отдельный `max_active_locations` удалён миграцией `00020`, ADR-0017), max_story_quests = 2 и max_side_quests = 1 — активных квестов на фракцию (миграция `00024`, пункт 28), max_active_goals, goal_cooldown_rounds, min_round_answers = 2 — кворум раунда, миграция `00005`; allow_resubmit = false — замена заявки, внутренняя настройка; manual_attributes = false — ручное распределение очков при создании персонажа, выключено в любом режиме (B30), внутренняя настройка; death_mode = fake и death_days = 10 — режим и срок [смерти персонажа](../game/death.md), пункт 07), `rules_version`, `llm_profile_id` → `llm_profiles` (nullable; миграция `00009`), `join_code`, `created_by_user_id` → `users`, `next_round_at`, `opening_scene` (завязка истории при `/begin`, миграция `00022`, ADR-0018), `world_setups jsonb` (варианты стартового мира от нулевого вызова и выбор администратора: `attempts`, `options[]`, `chosen_no`; nullable; миграция `00032`, ADR-0028; у игры `setup` `next_round_at` — время нулевого вызова тиком) | `join_code`; частичный `chat_id where status <> 'finished'` |
| `game_members` | участие пользователей | `game_id`, `user_id` → `users`, `role` (admin/player), `status` (active/left/kicked), `joined_at` | `(game_id, user_id)` |
| `factions` | стороны конфликта ([модель](../game/models/factions.md)) | `game_id`, `code` (good, evil или код фракции, созданной рассказчиком), `title`, `description`, `tone`, `relations jsonb`, `explored_since_reset` (счётчик исследования: клеток, открытых фракцией с последнего сброса; миграция `00023`, пункт 27; минус порог при выдаче квеста, ноль при его завершении — пункт 28), `lair_x`, `lair_y` (координаты логова сил зла, nullable: у `evil` один из углов `(±3, ±3)`, выбран кодом при `/create`; миграция `00035`, B42, [ADR-0031](../decisions/0031-evil-lair.md)) | `(game_id, code)` |
| `parties` | группы персонажей для общей истории ([модель](../game/models/party.md); миграция `00019`, пункт 19, [ADR-0016](../decisions/0016-parties.md)) | `id` (`GENERATED BY DEFAULT`: движок создаёт группы по идентификаторам, зарезервированным в снимке раунда `ReservePartyIDs`), `game_id`, `season_id`, `name`, `status` (active/disbanded), `leader_character_id` → `characters` (скрыт от игроков), `location_id` → `locations`, `motto`, `summary` | уникальный `(season_id, lower(name)) where status = 'active'`; число активных групп сезона ≤ `settings.max_parties` (проверяет код) |
| `characters` | персонажи | `game_id`, `season_id` → `seasons`, `user_id`, `model_id` → `character_models`, `nickname`, `archetype`, `appearance varchar(300)` (внешность, B41, миграция `00033`; рассказчику не передаётся), `public_bio varchar(100)`, `secret_bio varchar(100)`, `status` (active/waiting/inactive/dead), `missed_rounds`, `death_rounds_left` и `died_at` (ненастоящая смерть, миграция `00010`), `knowledge jsonb` (факты из `fact_learned`), `faction_id` → `factions`, `party_id` → `parties` (nullable, FK `ON DELETE SET NULL`, миграция `00019`), `hp`, `conditions jsonb`, `inventory jsonb`, `location_id`, `xp` | `(season_id, user_id)`; FK `(game_id, user_id)` → `game_members`; индекс `party_id` |
| `character_stats` | значения характеристик и навыков | `game_id`, `character_id`, `kind` (attribute/skill), `key` (из `character_models.definition`), `value` | `(character_id, kind, key)` |
| `seasons` | сезоны сериала; у остальных типов единственный сезон 1 | `game_id`, `season_no`, `title`, `status` (active/finished), `started_at`, `finished_at` | `(game_id, season_no)` |
| `locations` | клетки сетки мира ([модель](../game/models/location.md), [динамический мир](../game/world.md); миграция `00010`, сетка `00023`, пункт 27, [ADR-0019](../decisions/0019-world-generation.md)): стартовая из `genres.template.locations`, остальные от рассказчика | `game_id`, `x`, `y` (от стартовой `(0,0)`), `name` (подпись, не ключ), `description` (у `card` — карточка соседа), `stage` (card/detailed; без строки — «none»), `terrain`, `difficulty` (0–2, прибавляется к сложности проверок), `visited_at` (первый вход персонажа), `start` (стартовая клетка персонажей); выходы по направлениям считает код по соседям | `(game_id, x, y)` |
| `world_objects` | объекты клеток (миграция `00023`) | `game_id`, `location_id` → `locations`, `key` (латиница snake_case), `title`, `description`, `state`, `revealed`, `hidden_until_revealed`, `searched`, `approaches jsonb` ([модель](../game/models/location.md)) | `(location_id, key)` |
| `items` | переносимые предметы (таблица с миграции `00023`, поведение — пункт [30](../plan/telegram/30-npc-and-items.md)) | `game_id`, `key`, `title`, `kind`, `props jsonb`, владелец: ровно один из `location_id` / `object_id` / `character_id` (проверка базы) | `(game_id, key)` |
| `npcs` | персонажи мира без игрока (таблица с миграции `00023`, поведение — пункт 30, [модель](../game/models/npc.md)) | `game_id`, `location_id`, `key`, `title`, `faction_id`, `state`, `attitude jsonb`, `knowledge jsonb`, `stats jsonb` | `(game_id, key)` |
| `location_discoveries` | первые открытия клетки по фракциям (миграция `00023`; счётчик — `factions.explored_since_reset`) | `game_id`, `location_id`, `faction_id`, `round_id`, `character_id` | `(location_id, faction_id)` |
| `generation_jobs` | сохранённые ответы модели по ключу задания (ADR-0019): пишутся вне транзакции раунда, повтор раунда берёт готовый ответ; без внешних ключей (`FOR UPDATE` на игре в транзакции раунда заблокировал бы проверку ключа) | `game_id`, `round_id` (nullable: завязка), `role`, `input_hash` (версия промпта, роль, системная часть, вход, схема), `response jsonb` (`json`, `text`, `model`, токены, длительность) | `(game_id, coalesce(round_id, 0), role, input_hash)` |
| `rounds` | раунды (миграция `00008`) | `game_id`, `season_id`, `round_no`, `opens_at`, `deadline_at`, `closed_at`, `status` (open/frozen/skipped/resolving/resolved/published/failed; после пункта 07 заморозка и разрешение в одной транзакции → `published`), `seed`, `snapshot jsonb` (v2: режим, персонажи с характеристиками и инвентарём, локации с объектами, заявки; v3, пункт 19: `parties` с лидером, `max_parties`, `new_party_ids` — резерв идентификаторов для `party_split`, `party_names`; v4, пункт 27: только занятые клетки и их соседи с `x`, `y`, `stage`, `terrain`, `visited`, `start`, `discovered_by`; v5, пункт 32: `speeches` — реплики для рассказчика), `narration jsonb` (ответ рассказчика: `prompt_version`, `chronicle`, `scenes[{location_id, party, text, event_seqs}]` — `party` здесь подпись сцены «Группа (Локация)» или название локации, `patched`; миграция `00012`, пункт 09; пуст при шаблонной хронике), `skip_reason` (причина пропуска или сбоя), `attempts` (откатов закрытия; после 3 — `failed`) и `metrics jsonb` (`model.RoundMetrics`: длительности шагов, вызовы и токены, запасные пути, статусы заявок, `llm_reused`, `world_cells`, `world_templated`; миграция `00013`, пункт 10, [ADR-0013](../decisions/0013-round-resilience.md)) | `(game_id, round_no)`; частичный `game_id where status = 'open'` |
| `action_submissions` | заявки (миграция `00008`) | `game_id`, `round_id`, `character_id`, `raw_text`, `status` (pending/frozen; статусы разрешения — пункт 07), `submitted_at`, `updated_at`, `telegram_message_id` | `(round_id, character_id)`; при пропуске раунда `round_id` переносится в следующий |
| `speeches` | реплики персонажей на раунд (миграция `00031`, пункт 32, [ADR-0027](../decisions/0027-say.md)) | `game_id`, `round_id`, `character_id`, `text`, `submitted_at` | `(round_id, character_id)`; при пропуске или сбое раунда `round_id` переносится в следующий вместе с заявками |
| `resolved_actions` | намерения и итоги (миграция `00010`) | `game_id`, `round_id`, `submission_id`, `character_id`, `intent jsonb`, `status` (resolved/rejected/deferred/ambiguous), `outcome`, `reason`, `summary`, `checks jsonb` (бросок), `event_seqs`, `conflict_group` | `submission_id` |
| `world_events` | журнал событий (миграция `00010`; только добавляется) | `game_id`, `round_id`, `seq` (сквозной в игре), `type` (`character_moved`, `check_rolled`, `object_changed`, `fact_learned`, `condition_added`, `hp_changed`, `character_waited`, `help_given`, `action_rejected`, `character_died`, `character_returned`, `party_split`, `party_held`, `cell_discovered` (private, пункт 27), `cell_generated` и `object_created` (пакет мира, пишет сценарий раунда после вызова рассказчика; `/replay` их не сравнивает), `quest_issued`, `quest_stage_placed`, `quest_stage_done` (с наградой `reward.fact` в знания адресатов), `quest_done`, `goal_revised` (пункт 28, private персонажам фракции, пишет сценарий раунда)), `payload jsonb`, `visibility` (public/location/private), `visible_to bigint[]`, `location_id`, `character_id` | `(game_id, seq)` |
| `messages_outbox` | исходящие (миграция `00008`, ADR-0009) | `game_id`, `round_id` (nullable), `telegram_chat_id`, `kind` (scene/round_open/round_closed/system/chronicle/private — приватное уведомление о заявке шаблоном; character_joined — объявление о новом персонаже без `round_id`, ADR-0022), `part`, `text`, `entities` (jsonb, разметка текста отрезками в рунах, NULL без неё; миграция `00034`, ADR-0030), `keyboard` (jsonb, кнопки под сообщением, миграция `00018`, ADR-0015), `status` (pending/sending/sent/failed/unknown), `telegram_message_id`, `attempts`, `send_after`, `claimed_at`, `last_error`, `sent_at` | дедуп по `(round_id, telegram_chat_id, kind, part)` при заданном `round_id` |
| `quests` | квесты фракций (миграция `00024`, пункт 28, [ADR-0020](../decisions/0020-quests.md), [модель](../game/models/quest.md)) | `game_id`, `faction_id`, `kind` (story/side), `status` (active/done), `title`, `summary`, `created_round_id` (nullable: завязка), `done_round_id` | у фракции активных story ≤ `settings.max_story_quests` (2), side ≤ `max_side_quests` (1) — проверяет код |
| `quest_stages` | этапы квеста (миграция `00024`) | `game_id`, `quest_id`, `stage_no`, `key`, `title`, `summary`, `distance` (клеток от предыдущего этапа), `condition jsonb` (`{kind, object, state, fact}`; вид из каталога `object_state` / `fact` / `visited`, конкретика после привязки), `reward jsonb` (`{fact}`), `location_id` (nullable до привязки), `status` (pending/bound/done), `done_round_id` | `(quest_id, stage_no)`, `(quest_id, key)` |
| `faction_goals` | цели фракций, строка на версию (миграция `00024`) | `game_id`, `faction_id`, `version`, `text`, `stage`, `quest_id` (сюжетный квест, по закрытию которого цель пересмотрена; nullable), `round_id` | `(faction_id, version)` |
| `llm_runs` | прогоны LLM (миграция `00009`; ответ, взятый из `generation_jobs`, прогона не создаёт) | `game_id` (nullable: `/llm test` вне игры), `round_id` (nullable), `profile_id`, `provider`, `role` (interpreter/narrator/opening/test), `model`, `prompt_version`, `tokens_in`, `tokens_out`, `cost_usd`, `subscription bool`, `duration_ms`, `error`, `request_id` | |

## Таблицы поздних этапов

| Таблица | Назначение | Ключевые поля | Уникальность | Пункт |
|---|---|---|---|---|
| `character_goals` | личные сюжетные цели | `game_id`, `character_id`, `text`, `status` (active/achieved/abandoned), `created_at`, `achieved_at`, `abandoned_at` | лимит активных по `settings.max_active_goals`; новая не раньше `goal_cooldown_rounds` после отказа | [17](../plan/telegram/17-story-goals.md) |
| `goal_steps` | шаги цели | `game_id`, `goal_id`, `step_no` (1–10), `text`, `status` (pending/done), `done_round_id`, `stalled_rounds` | `(goal_id, step_no)` | 17 |

## Инварианты

- Внутри БД связи по `id`; идентификаторы Telegram (`telegram_id`, `telegram_chat_id`) только в `users`, `chats`, `messages_outbox`.
- В чате не более одной незавершённой игры; завершённые остаются историей. У игры всегда есть активный сезон; у `global` и `story` он единственный.
- Персонаж создаётся только у активного участника игры; у пары (сезон, пользователь) не более одного персонажа. Ключи `character_stats` только из модели персонажа жанра.
- На старте у игры две фракции, новые создаёт рассказчик по сюжету; персонаж всегда в одной фракции. Активных групп в сезоне не больше `max_parties`; размер группы не ограничен; персонаж не более чем в одной группе сезона. Расхождение участников по локациям создаёт новую группу (`party_split`) только в пределах лимита, иначе движок удерживает группу вместе (`party_held`, [группы](../game/parties.md)).
- `secret_bio` и `public_bio` — текст игрока: в промпт попадают как данные в выделенном блоке, `secret_bio` в контекст рассказчика только как данные для подачи линии персонажа, в хронике не раскрывается.
- Состояние персонажей и клеток производно от `world_events`: сценарий раунда применяет события свёрткой `engine.Apply` и сохраняет результат; прямые правки без события запрещены (исключения — пропуски и счётчик смерти при заморозке, они не события мира, и стартовый пакет при `/begin`: нулевой снимок до первого раунда). У каждой занятой клетки к концу раунда есть четыре соседа хотя бы в стадии `card`.
- У каждой заявки раунда ровно одна строка в `resolved_actions` после разрешения.
- `rounds.snapshot` хранит версию состояния, список заявок и реплик на момент дедлайна; воспроизведение движка по снимку и `seed`. У игры не более одного открытого раунда; `games.next_round_at` — единственный таймер планировщика (ADR-0009).
- Повторная отправка outbox не создаёт второе сообщение: `telegram_message_id` сохраняется, неопределённый исход помечается и перепроверяется.

## Открыто

Хранение «знаний персонажа» (отдельная таблица `character_knowledge` или факты в событиях); срок хранения `raw_text` (G8).
