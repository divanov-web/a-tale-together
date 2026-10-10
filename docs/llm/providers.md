# Провайдеры LLM

Все запросы к ИИ идут через один интерфейс; реализация выбирается настройками бота и игры из БД. Решение: [ADR-0003](../decisions/0003-llm-provider.md). Код: `internal/adapter/llm/*`, правило `.claude/rules/llm.md`.

## Интерфейс

```go
// Client — единый вход для интерпретатора и рассказчика. Реализации в internal/adapter/llm/<provider>.
type Client interface {
    Complete(ctx context.Context, req Request) (Response, error)
}

type Request struct {
    Role         string          // interpreter | narrator: влияет на модель профиля
    System       string          // стабильная часть промпта (правила, схема)
    Input        string          // переменная часть: контекст и данные игрока
    JSONSchema   json.RawMessage // непустая → ответ обязан соответствовать схеме
    MaxOutput    int
    Temperature  *float64        // nil → провайдер по умолчанию
}

type Response struct {
    Text       string          // текст ответа (рассказчик)
    JSON       json.RawMessage // структурированный ответ (интерпретатор)
    Model      string
    TokensIn   int
    TokensOut  int
    CostUSD    float64         // оценка; 0 для подписки
    RequestID  string
}
```

Интерфейс объявляется у потребителей (`service/interpreter`, `service/narrator`) и расширяется только через ADR.

## Реализации

| Ключ | Реализация | Оплата | Назначение | Статус |
|---|---|---|---|---|
| `claude-cli` | запуск `claude -p` локально, вывод JSON | подписка Claude Max, без оплаты токенов | локальная разработка и тестовый бот | план |
| `anthropic-api` | `anthropic-sdk-go`, Messages API | API-кредиты Max ($100–200 в месяц) или платно | прод-бот | план |
| `openai-api` | `openai-go`, Responses API со structured outputs | платно | альтернатива, сравнение качества | план |
| `fake` | записанные ответы из `testdata/` | нет | тесты, eval без сети | план |

Новый провайдер: пакет `internal/adapter/llm/<ключ>`, регистрация в реестре `llm.Registry`, строка в этой таблице, ADR при новой зависимости.

### claude-cli

Проверено по документации Claude Code (2026-10-10):

```bash
claude -p --output-format json --json-schema '<схема>' \
  --system-prompt '<системная часть>' --tools "" \
  --no-session-persistence --model <модель> --effort low \
  --max-budget-usd 0.5 < input.txt
```

- Ответ: JSON с полями `result` (текст), `structured_output` (по схеме), `session_id`, `total_cost_usd`, `usage`. Код выхода ≠ 0 — ошибка.
- `--tools ""` запрещает инструменты; `--system-prompt` заменяет системный промпт Claude Code целиком; `--no-session-persistence` не пишет сессию на диск.
- Без `--bare` CLI подхватывает `CLAUDE.md`, скиллы и хуки рабочей папки: запускать из отдельной пустой папки (`CLAUDE_CLI_WORKDIR`). С `--bare` подписка не используется, нужен `ANTHROPIC_API_KEY`, поэтому `--bare` для этого провайдера не подходит.
- Стоимость: `total_cost_usd` — оценка по тарифам API; при подписке реальная плата 0, но лимиты окна подписки расходуются. В `llm_runs` писать оценку и признак `subscription`.
- Ограничения: один процесс на вызов (секунды на старт), параллелизм 1–2, таймаут 120 с, повтор при пустом `structured_output`.
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

Порядок выбора: профиль игры → глобальный профиль по умолчанию → ошибка конфигурации (раунд не запускается). Профили кэшируются в памяти и перечитываются при изменении.

Переключение (админка MVP = команды владельца бота в личке): `/llm` показать профили и текущий, `/llm use <profile> [game_id]`, `/llm add ...` — пункт плана [06](../plan/06-local-tokens.md). Веб-админка в MVP не планируется.

Секреты (ключи API, путь к CLI) только в `.env`/окружении, в БД не хранятся; профиль ссылается на провайдер, провайдер берёт ключ из конфига.

## Учёт

Каждый вызов → `llm_runs` с `profile_id`, `provider`, моделью, токенами, `cost_usd`, признаком подписки, длительностью, ошибкой. Отчёт стоимости по игре и профилю — пункт плана 13.
