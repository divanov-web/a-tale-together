# 33. Токены и цена каждого вызова LLM

- Статус: готово (2026-10-15, [ADR-0032](../../decisions/0032-llm-cost-accounting.md), миграция `00036`)
- Этап концепта: 2
- Зависит от: 06 (`llm_runs`), 09а (журнал), 10 (метрики раунда)

## Цель
Каждый запрос к модели — в боте и в консольных оценочных наборах — оставляет в учёте число токенов по видам и цену в долларах, посчитанную ботом по собственной таблице тарифов. Таблица живёт в БД рядом с профилями и учётом вызовов (решение руководителя 2026-10-15: тарифы в БД, использовать существующие таблицы), обновляется миграцией данных по команде руководителя; раунд и игра получают сумму. Расходы фильтруются по модели, роли (интерпретатор, рассказчик, в будущем художник и другие), датам и игре — всё это колонки `llm_runs`.

## Критерий готовности
Строка `llm call` в `logs/bot.log`, запись `llm` в журнале чата и строка `llm_runs` содержат токены по видам (все входные, запись кэша, чтение кэша, выход), `cost_usd` по таблице `llm_prices` и `cost_provider_usd`; `make eval-interpreter` и `make eval-narrator` печатают токены и цену по каждому вызову (`-v`) и итого; метрики раунда и карточка `/round` показывают цену раунда; неизвестная модель даёт цену 0 и предупреждение в логе; оценка провайдера сохраняется рядом для сверки. Команды бота не добавляются (решение руководителя 2026-10-15).

## Шаги
| № | Шаг | Слой / пакеты | Артефакты | Статус |
|---|---|---|---|---|
| 1 | Таблица тарифов `llm_prices` в БД: модель (полное имя или префикс датированных снимков) → USD за 1M токенов входа, записи кэша (5 мин), чтения кэша, выхода; источник и дата сверки. Сид миграцией с тарифами Anthropic на 2026-10-15 (Opus 5.5, Sonnet 5.5, Haiku 5.5, Fable 5.1, Opus 5, Opus 4.8, Sonnet 5), обновление — миграцией данных после сверки через `/claude-api`; процедура в `providers.md`. ADR: таблица в БД против файла в репозитории, без истории тарифов | `migrations/00036`, `docs/decisions` | ADR-0032, `llm_prices` | готово |
| 2 | Расчёт: `model.LLMUsage` (input, cache_write, cache_read, output) в `LLMResponse.Usage`; адаптер `claudecli` заполняет из `usage`, `total_cost_usd` → `ProviderCostUSD`; `model.LLMPrice.Cost`, `LLMPriceTable.Estimate(model, usage)` — точное имя, затем самый длинный префикс; без БД — пригоден для консоли | `model`, `adapter/llm/claudecli`, `adapter/llm/fake` | `model/llm_test.go`, тест адаптера | готово |
| 3 | Учёт в боте: `llm_runs` получает `tokens_cache_write`, `tokens_cache_read`, `cost_provider_usd`; `cost_usd` — по таблице (кэш тарифов в `app/llm` вместе с профилями); строка `llm call` и запись журнала `llm` — токены по видам, обе оценки; неизвестная модель — `warn` раз на модель; ответ из `generation_jobs` бесплатен | `app/llm`, `adapter/postgres`, `adapter/postgres/fake`, `adapter/chatlog` | тесты сервиса и хранилища | готово |
| 4 | Раунд: `RoundMetrics.CostUSD` суммой по шагам, в логе `round metrics`, `rounds.metrics` и карточке `/round`; завязка и генерация мира — `cost_usd` в своих строках лога и журнале | `service/interpreter`, `service/narrator` (`Result.CostUSD`), `app/round`, `app/bot` | тесты метрик (существующие) | готово |
| 5 | Консоль: `cmd/eval-*` берут тарифы из БД через `cmd/internal/evalcost`, печатают по вызову (`-v`: модель, токены по видам, обе оценки) и итого (цена набора); `/llm test` показывает токены по видам и обе оценки | `cmd/eval-interpreter`, `cmd/eval-narrator`, `cmd/internal/evalcost`, `app/bot/cmd_llm.go` | `make eval-interpreter EVAL_ARGS="-v -case <имя>"` | готово |
| 6 | Документы: `providers.md` («Учёт»: колонки, тарифы, обновление, примеры SQL отчётов), `data-model.md`, `chat-logs.md`, `layers.md`, `telegram-commands.md`, readme пакетов, `rules/llm.md`, `rules/postgres.md`, `/tale-debug` | docs, `.claude` | | готово |

## Проверка
`make test`, `make test-integration` (таблица тарифов из миграции находит датированный снимок); `make migrate` на `tale` и `tale_test`; `make restart` → `make logs` без `409 Conflict`; `SELECT id, role, model, tokens_in, tokens_cache_write, tokens_cache_read, tokens_out, cost_usd, cost_provider_usd FROM llm_runs ORDER BY id DESC LIMIT 5` — у новых строк обе оценки близки; `make eval-interpreter EVAL_ARGS="-v -case <имя>"` — токены по видам и цена у каждого вызова, итог набора.

## Решения
- Тарифы в БД (`llm_prices`), не в файле репозитория; история — в миграциях; без `valid_from` ([ADR-0032](../../decisions/0032-llm-cost-accounting.md)).
- `tokens_in` остаётся суммой всех входных токенов (совместимость со старыми строками и метриками); разбивка — отдельными колонками.
- Прежние строки `llm_runs`: оценка провайдера скопирована в `cost_provider_usd`, `cost_usd` не пересчитана (нет разбивки по видам).
- При подписке реальная плата 0 — цена остаётся оценкой расхода окна подписки (как в `providers.md`). Отчёт стоимости по игре и профилю остаётся в пункте 13 и опирается на `llm_runs`.
