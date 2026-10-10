# Слоёная архитектура

Слои ровно под требования этапов 0–2 roadmap: Telegram-бот, раунды по расписанию, детерминированный движок, два вида вызовов LLM, PostgreSQL как хранилище и очередь. Новый пакет появляется с требованием, которое не ложится в существующие; новый слой только через ADR.

## Слои и направление зависимостей

```text
cmd/bot ──────────────► internal/app ──► internal/service ──► internal/model
   │                        │                   │
   │                        └── интерфейсы ◄────┘  (объявляет потребитель)
   └──► internal/adapter (реализует интерфейсы, импортирует только model и внешние SDK)
   └──► internal/config
```

| Слой | Пакеты | Отвечает | Импортирует | Не импортирует |
|---|---|---|---|---|
| Запуск | `cmd/bot` | конфиг, логгер, сборка зависимостей, фоновые циклы, остановка по сигналу | всё | |
| Сценарии | `internal/app/*` | порядок шагов прикладного действия, границы транзакций, вызов сервисов, запись в outbox | `service`, `model` | SQL, Telegram API, SDK LLM |
| Логика | `internal/service/*` | правила, разрешение раунда, видимость, сборка контекста и валидация ответов LLM | `model` | `app`, `adapter`, БД, Telegram |
| Модель | `internal/model` | типы сущностей, событий, намерений, статусов; без поведения с I/O | ничего внутреннего | |
| Адаптеры | `internal/adapter/*` | SQL, Telegram API, API LLM | `model`, внешние SDK | `app`, `service` |
| Конфиг | `internal/config` | дефолты, env, валидация | | |

Интерфейсы Go неявные: адаптер реализует интерфейс потребителя, не импортируя его пакет; связывает их `cmd/bot`.

## Пакеты MVP

| Пакет | Публичный вход | Суть |
|---|---|---|
| `app/bot` | `Register(bot, handlers)` | команды Telegram → разбор аргументов → `game_id` по чату → вызов `app/game`; ответы шаблонами |
| `app/chat` | `Register`, `BotStatusChanged`, `SetOwner`, `AddModerator`, `RemoveModerator`, `CanManage` | чаты с ботом, владелец и модераторы, проверка прав (ADR-0007) |
| `app/game` | `CreateGame`, `Join`, `CreateCharacter`, `SubmitAction`, `Status`, `Pause` | сценарии управления игрой и заявками; права через `app/chat` |
| `app/round` | `Run(ctx, gameID)` | заморозка → интерпретация → разрешение → фиксация → описание → outbox |
| `app/scheduler` | `Run(ctx)` | тик: игры с `next_round_at <= now` в активном окне → `round.Run` под блокировкой |
| `app/delivery` | `Run(ctx)` | воркер outbox → отправка → `telegram_message_id`, повторы |
| `service/rules` | `Catalog`, `Difficulty`, `Version` | правила как данные: каталог действий, сложности, формулы |
| `service/engine` | `Resolve(snapshot, intents, rng) Result` | конфликтные группы, фазы, проверки, события; чистая функция |
| `service/visibility` | `Filter(events, viewer)` | события, доступные адресату |
| `service/interpreter` | `Parse(ctx, scene, submissions) []Intent` | контекст сцены → вызов `LLM` → валидация по каталогу |
| `service/narrator` | `Chronicle(ctx, ...)`, `Private(ctx, ...)` | тексты из разрешённых событий; шаблоны для стандартных исходов |
| `adapter/postgres` | `Store`, `InTx` | репозитории по сущностям, миграции goose |
| `adapter/telegram` | `Client` | источник update, отправка, деление длинных сообщений |
| `adapter/llm` | `Client.Complete` | провайдер по ADR-0003, учёт токенов |

## Интерфейсы

- Объявляет потребитель рядом с использованием: `app/round.Store`, `service/interpreter.LLM`, `app/delivery.Sender`. Узкие, 1–5 методов.
- Один адаптер реализует несколько узких интерфейсов; дробить адаптер по интерфейсам не нужно.
- Фейки живут в `_test.go` потребителя; при повторном использовании — `internal/adapter/<x>/fake`.

## Транзакции и состояние

- Границу транзакции держит сценарий: `store.InTx(ctx, func(tx Store) error)`. Сервисы о транзакциях не знают.
- Движок получает снимок и возвращает события; сценарий записывает события, итоги, состояние и outbox в одной транзакции.
- Раунд одной игры исполняется одним воркером: строка `rounds` под `FOR UPDATE SKIP LOCKED`.

## Поток раунда через слои

1. `app/scheduler` выбирает игру → `app/round.Run`.
2. `round` → `postgres.FreezeRound` (снимок, seed).
3. `round` → `interpreter.Parse` → `llm.Complete` → намерения.
4. `round` → `engine.Resolve` → события и статусы заявок.
5. `round` → `postgres.InTx`: события, итоги, состояние, `rounds.status`.
6. `round` → `visibility.Filter` → `narrator` → `llm.Complete` → тексты → outbox.
7. `app/delivery` → `telegram.Send`.

## Что сознательно не вводим

Слой domain с поведением сущностей, интерфейс-репозиторий на каждую таблицу, CQRS, полный event sourcing (журнал есть, состояние хранится явно), DI-контейнер, брокер сообщений, кэш. Пересмотр: ADR при появлении требования.
