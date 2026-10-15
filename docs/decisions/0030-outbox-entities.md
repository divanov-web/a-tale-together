# ADR-0030: Разметка игровых сообщений outbox и ключи карточек жирным

- Статус: принято
- Дата: 2026-10-15
- Область: `internal/model` (`text.go`, `round.go`), `migrations/00034`, `internal/adapter/postgres` (`outbox.go`), `internal/adapter/telegram`, `internal/app/delivery`, `internal/app/round`, `internal/app/bot`, `internal/app/game`; продолжает [ADR-0029](0029-text-entities.md)

## Контекст
Правило руководителя 2026-10-14 (B40): в системных сообщениях вида «ключ: значение» ключ жирным. Карточки и списки ответов на команды (`/status`, `/character`, `/party`, `/quests`, `/knowledge`, `/bio`, разведка, вопросы настройки) идут синхронно через `Messenger` и уже умеют разметку (ADR-0029). Личный итог раунда, личное и групповое «раунд открыт», объявление о новом персонаже идут через `messages_outbox`, где хранится только простой текст; ADR-0029 отложил их до первого запроса. Карточки `/status` и `/character` собирались `text/template`, который умеет только строки.

## Варианты
| Вариант | За | Против |
|---|---|---|
| Пост-обработка строк: «всё до первого `: ` в строке — ключ» | одна функция на все тексты, шаблоны остаются | текст игрока и модели в начале строки (заявка «крикнуть: стой», знания) ломает выделение; разметка зависит от содержимого, а не от сборки |
| Маркеры в шаблонах, снимаемые при отправке | шаблоны остаются | отвергнуто в ADR-0029: маркер в тексте игрока меняет вид сообщения |
| Явная сборка `TextBuilder` везде, карточки кодом вместо шаблонов (выбран) | ключ выделяется там, где пишется; текст в журнале и тестах прежний | два шаблона переписаны в Go; outbox нужна колонка и разбиение частей с учётом разметки |

## Решение
- **Модель.** `TextBuilder.Key(k)` пишет «k: » с жирным ключом, `Header(t)` — «t:» для заголовков списков, `Append` склеивает размеченные тексты со сдвигом, `Len` — длина в рунах; `FormattedText.Cut(from, to)` вырезает отрезок с обрезкой пересекающих выделений. `TextEntity` получает теги JSON. `OutboxMessage.Entities` и `Formatted()`.
- **Хранение.** `messages_outbox.entities jsonb` (миграция `00034`), NULL без разметки; `EnqueueOutbox` пишет, `scanOutbox` читает.
- **Доставка.** `delivery.Sender.Send` и `telegram.Client.Send` принимают `model.FormattedText`; `SendFormatted` слит в `Send`.
- **Разбиение.** `round.splitParts` делит `FormattedText`: части вырезаются `Cut` по границам строк, подпись продолжения сдвигает выделения. Хроника пока без разметки (проза рассказчика).
- **Тексты.** Ключи жирным: `/status`, `/character` (карточки кодом, `characterCard` по строкам `cardRows`; шаблоны `status.tmpl`, `character.tmpl` удалены), `/party`, `/quests`, `/knowledge`, `/bio`, `/scout` («Разведка», «Переход»), «Сейчас» в вопросах настройки, итог создания персонажа (`round.Welcome` возвращает `FormattedText`: «Завязка», «Последняя хроника», «Варианты хода»), личный итог раунда («Заявка «…»», «Проверка», «Шанс был», факты, «Следующая заявка»), «Варианты для вас» / «Варианты на раунд N» (заголовок и подходы), «раунд открыт» в группе и в личке («Заявки до …», «Соседние места», «Перенесено заявок…»), объявление о новом персонаже («Фракция», «Внешность», «Описание»).
- **Диалоги.** `question.text` — `model.FormattedText`; обычные вопросы через `ask`; вступления к вопросам — `prefixed`.

## Последствия
- Код: `model/text.go` (+ тест), `model/round.go`, `migrations/00034_outbox_entities.sql`, `adapter/postgres/outbox.go`, `adapter/postgres/fake`, `adapter/telegram/client.go`, `app/delivery/service.go`, `app/round/{texts,welcome,scout,chronicle,resolve,service}.go`, `app/game/character.go` (`JoinedNotice`), `app/bot/{service,dialog,dialog_setup,dialog_character,cmd_character,cmd_game,cmd_party,cmd_quests}.go`; шаблоны `templates/` остаются для `chat`, `game_created`, `help`, `me`, `start`.
- Документы: readme `model`, `adapter/telegram`, `adapter/postgres`, `app/delivery`, `app/round`, `app/bot`, `app/bot/templates`, `migrations`; `docs/architecture/data-model.md`; `rules/telegram.md`.
- Не входит: разметка хроники и шаблонов `chat`, `me`, `game_created` — по отдельному замечанию.
