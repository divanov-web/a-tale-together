# 06. Локальные токены: LLM через подписку Claude Code

- Статус: готово (2026-10-10)
- Этап концепта: 2
- Зависит от: 05, [providers.md](../../llm/providers.md), T1, T2

## Цель
Бот обращается к ИИ через единый интерфейс, первая реализация — провайдер `claude-cli` на локальной подписке Claude Max без оплаты токенов API. Профили провайдеров в БД, переключение командами владельца.

## Критерий готовности
Команда владельца `/llm test` получает структурированный JSON-ответ от `claude -p` по схеме; вызов записан в `llm_runs` с признаком подписки; профиль переключается для бота и для игры без перезапуска; при недоступном CLI бот отвечает понятной ошибкой, не падает.

## Шаги
| № | Шаг | Слой / пакеты | Артефакты | Статус |
|---|---|---|---|---|
| 1 | Интерфейс `Client.Complete(ctx, model.LLMRequest) (model.LLMResponse, error)` у потребителя `app/llm`, типы запроса и ответа в `model/llm.go`, реестр провайдеров по ключу (карта, собирается в `cmd/bot`), адаптер `fake` | `model`, `app/llm`, `adapter/llm/fake` | `app/llm/service_test.go` | готово |
| 2 | Адаптер `claude-cli`: `claude -p --output-format json --json-schema … --system-prompt … --tools "" --no-session-persistence` плюс флаги изоляции `--setting-sources "" --strict-mcp-config --mcp-config {} --exclude-dynamic-system-prompt-sections`; пустая рабочая папка `CLAUDE_CLI_WORKDIR` (создаётся при старте), таймаут и параллелизм, разбор `structured_output`, `result`, `total_cost_usd`, `usage`, `modelUsage` | `adapter/llm/claudecli` | тесты с подменой бинаря `testdata/claude`; живой тест `CLAUDE_CLI_LIVE=1` | готово |
| 3 | Таблицы `llm_profiles`, `app_settings`, поле `games.llm_profile_id`, таблица `llm_runs`; стартовый профиль `local-claude` (haiku / sonnet, effort low, бюджет $0.5, таймаут 120 с, параллелизм 2) и `default_llm_profile` в миграции | `migrations/00009`, `model`, `adapter/postgres` | интеграционный тест | готово |
| 4 | Выбор профиля: игра → `default_llm_profile` → `ErrNoProfile`; кэш профилей в памяти, сбрасывается при `use`, `reset`, `add` | `app/llm` | тесты | готово (кэш одного процесса; второй экземпляр бота не предусмотрен) |
| 5 | Команды владельца бота в личке (`BOT_OWNER_ID`): `/llm`, `/llm use <profile> [game_id]`, `/llm reset <game_id>`, `/llm add <name> <provider> <model_interpreter> <model_narrator> [effort]`, `/llm test [profile]` | `app/bot` | `TestLLM_OwnerCommands` | готово |
| 6 | Запись каждого вызова в `llm_runs` (профиль, провайдер, модель, версия промпта, токены, `cost_usd`, `subscription`, длительность, ошибка, `game_id`, `round_id`), в том числе при ошибке провайдера | `app/llm`, `adapter/postgres` | тесты | готово |
| 7 | Проверка подписки на практике: 6 вызовов по 2 параллельно и живой тест — без ошибок окна, 3–6 с на вызов; наблюдения в [providers.md](../../llm/providers.md) | проверка | отчёт | готово; 20 вызовов за раунд — проверить в пункте 10 |
| 8 | Заготовки `anthropic-api` и `openai-api`: пакеты в реестре, `ErrNotImplemented` до пункта 08 | `adapter/llm/anthropic`, `adapter/llm/openai` | | готово |

## Проверка
`/llm test` в личке владельца с живым CLI (ответ с моделью, токенами, оценкой стоимости и JSON); `CLAUDE_CLI_LIVE=1 go test ./internal/adapter/llm/claudecli -run Live`; `make test`, `make test-integration`.

## Открытые вопросы
T1 (профили по умолчанию), T2 (границы использования подписки). Путь к `claude` и рабочая папка на прод-хостинге, если там тоже понадобится `claude-cli`.
