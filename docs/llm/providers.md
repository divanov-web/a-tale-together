# Провайдеры LLM

Все запросы к ИИ идут через один интерфейс; реализация выбирается настройками бота и игры из БД. Решение: [ADR-0003](../decisions/0003-llm-provider.md). Код: `internal/adapter/llm/*`, правило `.claude/rules/llm.md`.

## Интерфейс

```go
// Client — единый вход; объявлен у потребителя app/llm, реализации в internal/adapter/llm/<provider>.
type Client interface {
    Complete(ctx context.Context, req model.LLMRequest) (model.LLMResponse, error)
}

// model.LLMRequest: Role (interpreter | narrator | test), Model, System (стабильная часть), Input (переменная часть,
// текст игрока только здесь), JSONSchema (непустая → ответ по схеме), MaxOutput, Temperature, Effort, MaxBudgetUSD, Timeout.
// model.LLMResponse: Text, JSON, Model, Usage (токены по видам: Input, CacheWrite, CacheRead, Output), CostUSD (по таблице
// llm_prices, заполняет app/llm), ProviderCostUSD (оценка провайдера; при подписке реальная плата 0), Subscription, RequestID, Duration.
```

Типы в `internal/model/llm.go`; интерфейс у потребителя (`app/llm`, позже `service/interpreter` и `service/narrator` берут `app/llm.Complete`) и расширяется только через ADR. Вызов через `app/llm.Complete(ctx, Call{Game, Round, Role, PromptVersion}, req)`: модель и параметры подставляются из профиля, вызов записывается в `llm_runs` даже при ошибке.

## Реализации

| Ключ | Реализация | Оплата | Назначение | Статус |
|---|---|---|---|---|
| `claude-cli` | запуск `claude -p` локально, вывод JSON | подписка Claude Max, без оплаты токенов | локальная разработка и тестовый бот | готово (пункт 06) |
| `anthropic-api` | `anthropic-sdk-go`, Messages API | API-кредиты Max ($100–200 в месяц) или платно | прод-бот | заглушка, реализация в пункте 08 |
| `openai-api` | `openai-go`, Responses API со structured outputs | платно | альтернатива, сравнение качества | заглушка, реализация в пункте 08 |
| `fake` | заданный ответ, запоминает запросы | нет | тесты | готово |

Новый провайдер: пакет `internal/adapter/llm/<ключ>`, регистрация в карте провайдеров в `cmd/bot/main.go`, строка в этой таблице, ADR при новой зависимости.

### claude-cli

Проверено на Claude Code 2.1.296 живыми вызовами (2026-10-10, пункт 06):

```bash
claude -p --output-format json --json-schema '<схема>' \
  --system-prompt '<системная часть>' --tools "" --no-session-persistence \
  --setting-sources "" --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --exclude-dynamic-system-prompt-sections \
  --model <модель> --effort low --max-budget-usd 0.5 < input.txt
```

- Ответ: JSON с полями `result` (текст), `structured_output` (по схеме), `session_id`, `is_error`, `total_cost_usd`, `usage` (`input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`), `modelUsage` (ключ — полное имя модели, например `claude-haiku-5-5` для псевдонима `haiku`). Код выхода ≠ 0 или `is_error` — ошибка; `stop_reason: tool_use` при структурированном ответе — норма.
- `--tools ""` запрещает инструменты; `--system-prompt` заменяет системный промпт Claude Code целиком; `--no-session-persistence` не пишет сессию на диск.
- Изоляция контекста (проверено): без дополнительных флагов в контекст попадают глобальный `~/.claude/CLAUDE.md` и инструкции MCP-серверов пользователя (около 5 000 токенов). `--setting-sources ""` отключает файлы настроек, `--strict-mcp-config --mcp-config '{"mcpServers":{}}'` — MCP, `--exclude-dynamic-system-prompt-sections` — блоки окружения; вместе остаётся около 1 200 токенов и модель подтверждает отсутствие CLAUDE.md, памяти и MCP. Рабочая папка `CLAUDE_CLI_WORKDIR` всё равно пустая и создаётся ботом при старте.
- `--bare` с подпиской не работает (проверено: `is_error: true`, 0 токенов; нужен `ANTHROPIC_API_KEY`), поэтому не используется.
- Стоимость: `total_cost_usd` — оценка CLI по тарифам API, пишется в `llm_runs.cost_provider_usd` для сверки; `cost_usd` бот считает сам по `llm_prices` (ADR-0032). При подписке реальная плата 0, но лимиты окна подписки расходуются — `subscription = true`.
- Ограничения: один процесс на вызов, параллелизм из настроек клиента (2), таймаут 120 с; пустой `structured_output` при заданной схеме — ошибка вызова (повтор решает потребитель).

Наблюдения (шаг 7 пункта 06): одиночный вызов haiku с небольшим промптом — 3–6 с, ~1 100 входных и 100–450 выходных токенов, оценка $0.0003–0.0012; 6 вызовов по 2 параллельно — 16 с без ошибок и ограничений окна. Нагрузка 20 вызовов за раунд проверяется в пункте 10.
- Условия использования: подписка Claude предназначена для Claude Code и Claude.ai; автоматический запуск официального CLI на своей машине допустим, передача OAuth-токена сторонним клиентам запрещена, для продуктов и сервисов Anthropic рекомендует API-ключ. Для прод-бота с чужими игроками использовать `anthropic-api`. Вопрос T2 в [open-questions](../game/open-questions.md).

### anthropic-api

- SDK `github.com/anthropics/anthropic-sdk-go`, аутентификация `ANTHROPIC_API_KEY`; при реализации подключать скилл `/claude-api`.
- Структурированный ответ: `output_config.format` с JSON-схемой; кэширование стабильного префикса через `cache_control`; модель по умолчанию `claude-opus-5-5`, для интерпретатора дешевле (`claude-haiku-5-5`) после измерений.
- Кредиты: на Claude Max с 2026-10-07 начисляются API-кредиты (Max 5x: $100, Max 20x: $200 в месяц), действуют на Messages API, Agent SDK и Managed Agents, не переносятся на следующий месяц, не работают на интерактивный Claude Code. Активация: привязать организацию Console на claude.ai (доступно через 7 дней подписки). Подтвердить в Help Center Anthropic перед расчётом бюджета.

### openai-api

- SDK `github.com/openai/openai-go/v3`, Responses API, `text.format` типа `json_schema` со `strict: true`. Ключ `OPENAI_API_KEY`.

## Выбор реализации: профили

| Таблица | Поля | Смысл |
|---|---|---|
| `llm_profiles` | `id`, `name`, `provider` (ключ), `model_interpreter`, `model_narrator`, `params jsonb` (effort, temperature, max_output, budget), `enabled` | набор настроек провайдера |
| `app_settings` | `key`, `value` | глобальные настройки бота, в том числе `default_llm_profile` |
| `games.llm_profile_id` | nullable FK | переопределение для конкретной игры |

Профили из миграций: `local-claude` (песочница) и `sim-haiku` (прогоны имитатора, haiku для всех ролей; миграция `00038`, ADR-0034 — имитатор ставит его глобальным в своей БД `tale_sim`).

Порядок выбора: профиль игры → глобальный профиль по умолчанию → ошибка конфигурации (раунд не запускается). Профили кэшируются в памяти `app/llm` и перечитываются после `use`, `reset`, `add`. Стартовый профиль `local-claude` (`claude-cli`, `effort low`, бюджет $0.5 на вызов, таймаут 120 с, параллелизм 2) и `default_llm_profile = local-claude` создаются миграцией `00009`; модели с миграции `00011` — интерпретатор `sonnet`, рассказчик `opus` (решение руководителя 2026-10-10: haiku давал 88–94 % на оценочном наборе, нестабильно на свободных испытаниях; для рассказчика важнее качество текста).

Переключение (админка MVP = команды владельца бота в личке, `BOT_OWNER_ID` в `.env`): `/llm` показать профили, провайдеры и профиль текущей игры; `/llm use <profile> [game_id]`; `/llm reset <game_id>`; `/llm add <name> <provider> <model_interpreter> <model_narrator> [effort]`; `/llm test [profile]` — живой вызов по схеме с моделью, токенами, оценкой и JSON в ответе ([руководство игрока](../../guide/commands.md)). Веб-админка в MVP не планируется.

Секреты (ключи API, путь к CLI) только в `.env`/окружении, в БД не хранятся; профиль ссылается на провайдер, провайдер берёт ключ из конфига.

## Учёт

Каждый вызов → `llm_runs` ([ADR-0032](../decisions/0032-llm-cost-accounting.md)): `profile_id`, `provider`, роль, модель, версия промпта, токены по видам (`tokens_in` = все входные: без кэша + `tokens_cache_write` + `tokens_cache_read`; `tokens_out`), `cost_usd` по таблице тарифов, `cost_provider_usd` (оценка провайдера), признак подписки, длительность, ошибка, `game_id` (NULL для `/llm test`), `round_id`, `created_at`. Та же разбивка и обе оценки — в строке `llm call` в `bot.log` и записи `llm` журнала чата; сумма по раунду — `rounds.metrics.cost_usd`, строка `round metrics`, карточка `/round`; завязка и генерация мира — `cost_usd` в своих строках лога. Ответ, взятый из `generation_jobs`, бесплатен. Отчёт стоимости по игре и профилю — пункт плана 13.

### Тарифы `llm_prices`

| Колонка | Смысл |
|---|---|
| `model` | полное имя модели (`claude-opus-5-5`) или префикс датированных снимков: поиск — точное имя, затем самый длинный префикс до дефиса |
| `input_usd`, `cache_write_usd`, `cache_read_usd`, `output_usd` | USD за 1M токенов: вход без кэша, запись кэша (пятиминутный тариф), чтение кэша, выход |
| `source`, `priced_at` | страница цен провайдера и дата сверки |

Стартовые тарифы (миграция `00036`, сверено 2026-10-15 со страницей цен Anthropic): Opus 5.5, Sonnet 5.5, Haiku 5.5 (до 100K токенов запроса), Fable 5.1, Opus 5, Opus 4.8, Sonnet 5. Неизвестная модель → `cost_usd = 0` и предупреждение `llm price unknown` в логе (одно на модель). Бот кэширует тарифы вместе с профилями и перечитывает после `/llm use`, `reset`, `add` и перезапуска.

Обновление по команде руководителя: Claude сверяет цены с официальными страницами (скилл `/claude-api`, `platform.claude.com/docs/en/about-claude/pricing`), пишет миграцию данных (`UPDATE` / `INSERT` в `llm_prices` с новой `priced_at`), `make migrate` на `tale` и `tale_test`, `make restart`. Старые строки `llm_runs` не пересчитываются: цена фиксируется в момент вызова; при необходимости пересчёт по токенам по видам.

Примеры отчётов (фильтры по модели, роли, датам, игре):

```sql
-- расход по ролям и моделям за период
SELECT role, model, count(*) AS calls, sum(tokens_in) AS tokens_in, sum(tokens_out) AS tokens_out,
       round(sum(cost_usd), 4) AS cost_usd, round(sum(cost_provider_usd), 4) AS provider_usd
FROM llm_runs WHERE created_at >= '2026-10-01' AND created_at < '2026-11-01' GROUP BY 1, 2 ORDER BY cost_usd DESC;
-- расход игры по раундам
SELECT round_id, role, round(sum(cost_usd), 4) FROM llm_runs WHERE game_id = 1 GROUP BY 1, 2 ORDER BY 1, 2;
-- последние вызовы с обеими оценками
SELECT id, role, model, tokens_in, tokens_cache_write, tokens_cache_read, tokens_out, cost_usd, cost_provider_usd FROM llm_runs ORDER BY id DESC LIMIT 5;
```

Консоль: `make eval-interpreter` и `make eval-narrator` берут тарифы из `DATABASE_URL` (без базы — предупреждение, цена 0), при `-v` печатают по каждому вызову модель, токены по видам и обе оценки, в итоге — цену набора; в `llm_runs` эти вызовы не пишутся.
