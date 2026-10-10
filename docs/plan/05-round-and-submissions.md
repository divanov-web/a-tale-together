# 05. Раунд и заявки

- Статус: готово (2026-10-10)
- Этап концепта: 1–2
- Зависит от: 04, `docs/game/round.md`, [ADR-0009](../decisions/0009-round-scheduler.md)

## Цель
Жизненный цикл раунда без разрешения: открытие по расписанию, приём и замена заявок, заморозка снимка, ручной запуск для тестов.

## Критерий готовности
Планировщик открывает раунды по настройкам игры; `/act` создаёт или заменяет заявку до дедлайна; по дедлайну раунд `frozen` со снимком и seed, а без кворума `skipped` (в dev с сообщением в группу, в prod молча); поздняя заявка попадает в следующий раунд; перезапуск бота не теряет и не дублирует раунды.

## Шаги
| № | Шаг | Слой / пакеты | Артефакты | Статус |
|---|---|---|---|---|
| 1 | Таблицы `rounds`, `action_submissions`; уникальность `(game_id, round_no)`, `(round_id, character_id)`, один открытый раунд на игру | `migrations/00008`, `model/round.go` | интеграционный тест `TestGameFlow` | готово |
| 2 | `Clock` и планировщик: тик, выбор игр с `next_round_at <= now`, игровое окно `play_from`/`play_to` по `timezone` (в том числе через полночь), длительность `round_minutes`, блокировка строки `LockGame` | `app/scheduler`, `app/round`, `adapter/postgres` | `round/service_test.go`, `TestWindow`, `scheduler/service_test.go` | готово |
| 3 | `/begin`: статус `active`, стартовая сцена из шаблона жанра с подстановкой мира и фракций, первый раунд (или его время вне окна); нужен хотя бы один персонаж | `app/round`, `app/bot` | `TestRound_BeginActPauseNext` | готово |
| 4 | `/act <текст>`: до 500 символов, замена не чаще раза в минуту, запись/замена заявки, подтверждение с номером раунда и дедлайном | `app/round`, `app/bot` | `TestSubmit_Validation` | готово |
| 5 | Заморозка: по дедлайну `frozen`, снимок персонажей и заявок, seed в `rounds`; заявки `frozen` | `app/round`, `adapter/postgres` | `TestTick_FreezeSnapshotAndActivity` | готово; локации в снимке — с пунктом 07 |
| 5б | Кворум раунда: заявок меньше `min_round_answers` → `skipped`, заявки переходят в следующий раунд, причина в `rounds.skip_reason` и `/status`; в dev сообщение «Недостаточное количество участников. Раунд пропущен» в группу (решение руководителя 2026-10-10) | `app/round`, `app/bot` | `TestTick_SkipWithoutQuorumAndCarrySubmissions` | готово |
| 5а | Статус персонажа по пропускам: `missed_rounds`, `active → waiting → inactive` (`inactive_after_rounds`), возврат заявкой | `app/round` | тест переходов в `TestTick_FreezeSnapshotAndActivity`, `TestRound_StatusTransitionsAndWindow` | готово; события `character_sidelined` / `character_returned` — пункт 07 |
| 6 | `/pause`: пауза и возобновление без догоняющих раундов (открытый раунд получает новый дедлайн); `/status` показывает текущий раунд, дедлайн, заявки, предыдущий раунд и причину простоя | `app/round`, `app/bot` | `TestPause_And_Next` | готово |
| 7 | Ручной запуск раунда админом для тестов: `/next` в группе только при `APP_ENV=dev` | `app/bot` | флаг `Settings.Env` | готово |
| 8 | Анонсы через outbox: таблица `messages_outbox`, воркер `app/delivery` с повторами, `telegram_message_id`, дедуп по `(round_id, chat, kind, part)`; сообщения: стартовая сцена, открытие раунда, закрытие/пропуск, пауза | `app/delivery`, `adapter/telegram` | `delivery/service_test.go`, интеграционный тест | готово; деление длинных текстов и хроника — пункт 09 |

## Проверка
Раунды по 10 минут в песочнице (`/set round_minutes 10`), `/next` для ускорения; заявки до и после дедлайна; перезапуск бота в середине раунда (`make restart`): раунд и `next_round_at` в БД, планировщик продолжает. Автотесты: `make test`, `make test-integration`.

## Открытые вопросы
G2 (лимит заявок), G7 (поток в последнюю минуту), G9 (пробный раунд). Границы рассказа и `/finish` — пункт 10, шаг 6а.
