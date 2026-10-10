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
| Запуск | `cmd/bot`, `cmd/eval-interpreter` | конфиг, логгер, сборка зависимостей, фоновые циклы, остановка по сигналу; прогон оценочного набора интерпретатора | всё | |
| Сценарии | `internal/app/*` | порядок шагов прикладного действия, границы транзакций, вызов сервисов, запись в outbox | `service`, `model` | SQL, Telegram API, SDK LLM |
| Логика | `internal/service/*` | правила, разрешение раунда, видимость, сборка контекста и валидация ответов LLM | `model` | `app`, `adapter`, БД, Telegram |
| Модель | `internal/model` | типы сущностей, событий, намерений, статусов; без поведения с I/O | ничего внутреннего | |
| Адаптеры | `internal/adapter/*` | SQL, Telegram API, API LLM | `model`, внешние SDK | `app`, `service` |
| Конфиг | `internal/config` | дефолты, env, валидация | | |

Интерфейсы Go неявные: адаптер реализует интерфейс потребителя, не импортируя его пакет; связывает их `cmd/bot`.

## Пакеты MVP

| Пакет | Публичный вход | Суть |
|---|---|---|
| `app/bot` | `New(...)`, `Service.Handle`, `Service.HandleChatMember`, `Service.RegisterMenu` | реестр команд с контекстами, лимит частоты, диалоги в личке (`dialogs`), вызов `app/chat` и `app/game`; ответы шаблонами `app/bot/templates` |
| `app/chat` | `BotStatusChanged`, `Ensure`, `SetOwner`, `AddModerator`, `RemoveModerator`, `CanManage`, `Info` | чаты с ботом, владелец и модераторы, проверка прав (ADR-0007) |
| `app/game` | `Create`, `Set`, `Join`, `Current`, `Status`, `CreateCharacter`, `SetBio`; позже `SubmitAction`, `Pause` | сценарии управления игрой, участниками и персонажами; права через `app/chat` |
| `app/round` | `Begin`, `Pause`, `Next`, `Submit`, `Info`, `Replay`, `Tick(ctx, now)` | жизненный цикл раунда (ADR-0009): открытие по окну, заявки, заморозка со снимком v2 и seed или пропуск без кворума; разрешение (`resolve.go`): типовые команды → `engine.Resolve` → `world_events`, `resolved_actions`, свёртка состояния, хроника и уведомления о заявке шаблоном в outbox; счётчик ненастоящей смерти |
| `app/scheduler` | `Run(ctx)` | тик по интервалу: `round.Tick` для игр с `next_round_at <= now`, каждая под блокировкой строки |
| `app/delivery` | `Run(ctx)`, `Deliver(ctx)` | воркер outbox → `telegram.Send` → `telegram_message_id`, повторы с паузой, `unknown` при неопределённом исходе |
| `service/rules` | `ValidateDraft`, `AutoDistribute`, `HP`, `Parse*`, `Catalog`, `Pool`, `ChancePct`, `Difficulty`, `Preset`, `ParseTyped`, `DeathRounds` | правила как данные: модель персонажа, каталог действий, пул и шансы, таблица риска, пресеты режимов, типовые команды |
| `service/engine` | `Resolve(snapshot, intents, seed) Result`, `Apply(snapshot, events)`, `Clone` | фазы, проверки с seed, события, ровно один статус на заявку; свёртка событий в состояние; чистые функции (конфликтные группы — пункт 12) |
| `service/visibility` | `Filter(events, viewer)`, `Public(events)` | события, доступные адресату; события для хроники |
| `service/interpreter` | `New(LLM, logger)`, `Interpret(ctx, snapshot, def) (Result, error)`, `BuildInput`, `Fallback`, `Schema`, `PromptVersion` | контекст сцен из снимка → один вызов `LLM` на все заявки → валидация по снимку и каталогу → повтор → безопасное толкование; `Fallback` — разбор кодом при сбое (ADR-0010) |
| `service/narrator` | `Chronicle(ctx, ...)`, `Private(ctx, ...)` | тексты из разрешённых событий; шаблоны для стандартных исходов |
| `adapter/postgres` | `Store`, `InTx` | репозитории по сущностям, миграции goose; `fake` — хранилище в памяти для тестов |
| `adapter/telegram` | `Client` | источник update, отправка, деление длинных сообщений |
| `app/llm` | `List`, `Profile`, `Resolve`, `Use`, `Reset`, `Add`, `Complete`, `Test` | профили LLM из БД с кэшем, карта провайдеров по ключу, подстановка модели и параметров профиля, запись `llm_runs` |
| `adapter/llm/*` | `Client.Complete` | провайдеры по ADR-0003: `claudecli`, `anthropic` и `openai` (заглушки до пункта 08), `fake` |

## Интерфейсы

- Объявляет потребитель рядом с использованием: `app/round.Store`, `service/interpreter.LLM`, `app/delivery.Sender`. Узкие, 1–5 методов.
- Один адаптер реализует несколько узких интерфейсов; дробить адаптер по интерфейсам не нужно.
- Фейки живут в `_test.go` потребителя; при повторном использовании — `internal/adapter/<x>/fake`.

## Транзакции и состояние

- Границу транзакции держит сценарий: `store.InTx(ctx, func(ctx context.Context) error)`; транзакция передаётся через контекст, методы хранилища берут её оттуда (ADR-0008). Сервисы о транзакциях не знают.
- Движок получает снимок и возвращает события; сценарий записывает события, итоги, состояние и outbox в одной транзакции.
- Раунд одной игры исполняется одним воркером: строка `rounds` под `FOR UPDATE SKIP LOCKED`.

## Поток раунда через слои

1. `app/scheduler` → `app/round.Tick`: игра с наступившим `next_round_at` под `LockGame`.
2. `round` → `postgres.FreezeRound` (снимок, seed).
3. `round` → `interpreter.Parse` → `llm.Complete` → намерения.
4. `round` → `engine.Resolve` → события и статусы заявок.
5. `round` → `postgres.InTx`: события, итоги, состояние, `rounds.status`.
6. `round` → `visibility.Filter` → `narrator` → `llm.Complete` → тексты → outbox.
7. `app/delivery` → `telegram.Send`.

## Что сознательно не вводим

Слой domain с поведением сущностей, интерфейс-репозиторий на каждую таблицу, CQRS, полный event sourcing (журнал есть, состояние хранится явно), DI-контейнер, брокер сообщений, кэш. Пересмотр: ADR при появлении требования.
