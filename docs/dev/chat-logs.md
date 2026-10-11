# Журнал чатов

Файловый журнал для разбора ошибок ИИ ([ADR-0012](../decisions/0012-chat-logs.md)): по нему без БД восстанавливается обмен по чату, вызовы LLM и события раунда. Пишут сценарии `app/bot`, `app/delivery`, `app/llm`, `app/round` через `Journal`; пишет `adapter/chatlog`. Включён по умолчанию (`CHAT_LOG_DIR=logs/chats`, `CHAT_LOG_RETENTION_DAYS=30`); пустой `CHAT_LOG_DIR` выключает. Содержит тексты игроков и скрытые описания персонажей: только локально, `logs/` в `.gitignore`.

## Структура
`logs/chats/<telegram_chat_id>/<ГГГГ-ММ-ДД>.jsonl` — папка на чат (группа игры: отрицательный ID; личка: ID пользователя), файл на день, запись в строке. Поля записи `model.ChatLogRecord`:

| Поле | Содержание |
|---|---|
| `ts`, `kind`, `chat_id` | время, вид (`in`, `out`, `llm`, `round`, `error`), чат; у нажатия кнопки `event: callback`, `text` — данные кнопки |
| `update_id`, `duplicate`, `user`, `command`, `text` | входящее: update, повтор, автор (`id`, `username`, `name`), команда, текст |
| `game_id`, `round_id`, `round_no` | игра и раунд; у входящих в личке — текущая игра игрока |
| `event` | `in`: `my_chat_member`, `rate_limited`; `round`: `opening` (завязка при `/begin`, без раунда), `opened`, `submission`, `skipped`, `frozen`, `interpreted`, `narrated`, `resolved`, `published`, `metrics`, `attempt_failed`, `failed`, `paused`, `resumed`, `finished`, `reset` |
| `attempt` (у `round`) | номер попытки закрытия раунда: записи прерванной попытки остаются в журнале, в БД фиксируется только успешная ([ADR-0013](../decisions/0013-round-resilience.md)) |
| `outbox_id`, `outbox_kind`, `part`, `attempt`, `telegram_message_id`, `error` | исходящее outbox: попытка отправки; `out` без `outbox_id` — синхронный ответ на команду |
| `llm` | `role`, `prompt_version`, `profile`, `provider`, `model`, `system`, `input`, `output`, `tokens_in`, `tokens_out`, `duration_ms` |
| `data` | подробности события раунда: `opening` — `calls`, `issues`, `text` или `fallback` с `error`; `opened` — `deadline_at`, `carried`; `submission` — персонаж и текст; `skipped` — `reason`; `frozen` — `seed`, заявки с никнеймами, статусы персонажей с `party_id`; `interpreted` — `intents`, `calls`, `safe` или `fallback` с `error`; `narrated` — `narration`, `calls`, `patched`, `issues` или `fallback`; `resolved` — `results`, `events`; `published` — `chronicle_parts`, `notices`; `metrics` — `model.RoundMetrics` (миллисекунды шагов, вызовы и токены, запасные пути, статусы заявок); `attempt_failed` — `attempt`, `error` (откат транзакции закрытия); `failed` — `attempts`, `error` (раунд не разрешён, заявки перенесены); `paused` — `reason` (`manual` или `skipped_rounds` с числом `skipped`, B11); `finished` — `reason`, `rounds`; `reset` — `characters` |

Где что искать: вызовы LLM и события раунда — в папке группы игры; `/act` игрока — `in` в его личке (текст без команды, принятый как заявка, — ещё запись `in` с `event: implicit_act`) или в группе, и `submission` в группе; уведомление о заявке — `out` с `outbox_kind: private` в личке, личное «раунд открыт» с вариантами — `private_open` (B15); хроника — `out` с `outbox_kind: chronicle` по частям в группе.

## Разбор сбоя
1. Чат и день: группа игры по `/status` или `chats.telegram_chat_id`; личка — Telegram ID игрока (`/me`).
2. Раунд: `grep '"round_no":N' logs/chats/<chat>/<день>.jsonl` даёт цепочку `opened → submission → frozen → interpreted → narrated → resolved → published → metrics`; обрыв цепочки показывает шаг сбоя, `error` в записи — причину; `attempt_failed` — попытка откатилась и повторится следующим тиком (стек паники в `logs/bot.log`), `failed` — раунд закрыт со сбоем после трёх попыток. В Telegram то же показывает dev-команда `/round N`.
3. Намерения против заявок: `jq -c 'select(.event=="frozen") | .data.submissions' …` и `jq -c 'select(.event=="interpreted") | .data.intents' …`; расхождение — промпт интерпретатора или валидация.
4. Вход и выход модели: `jq -r 'select(.kind=="llm" and .round_id==ID) | .llm.input, .llm.output' …`; дозапрос виден как вторая запись той же роли.
5. Хроника и доставка: `jq -c 'select(.kind=="out" and .round_id==ID) | {outbox_id,part,attempt,telegram_message_id,error}' …`; `error` без последующей записи с `telegram_message_id` — сообщение в `failed`.
6. Команда игрока: `grep '"update_id":N'` в личке — входящее, ответ `out` и `error` сценария одним `update_id`.

Инструмент `jq` не обязателен: все поля ищутся `grep` по подстроке `"ключ":значение`.
