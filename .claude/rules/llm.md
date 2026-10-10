---
paths:
  - "internal/adapter/llm/**"
  - "internal/service/interpreter/**"
  - "internal/service/narrator/**"
  - "assets/prompts/**"
---
# LLM в коде

- Источник: `docs/llm/round-calls.md` (приоритет при расхождениях: два вызова на раунд, входы и выходы), `docs/llm/providers.md` (интерфейс `llm.Client`, провайдеры, профили), ADR-0003. Дизайн промптов: `/tale-prompts`.
- Один интерфейс `Complete(ctx, model.LLMRequest) (model.LLMResponse, error)` у потребителя (`app/llm.Client`); реализации в `internal/adapter/llm/<provider>`; карта провайдеров по ключу собирается в `cmd/bot`; выбор профиля `app/llm.Resolve`: игра → `app_settings.default_llm_profile`; интерпретатор и рассказчик вызывают модель только через `app/llm.Complete(ctx, Call, req)` — он подставляет модель и параметры профиля и пишет `llm_runs`.
- Провайдеры: `claude-cli`, `anthropic-api`, `openai-api`, `fake`. Новый провайдер = пакет + регистрация + строка в `providers.md`; новая зависимость = ADR.
- `claude-cli`: `claude -p --output-format json --json-schema … --system-prompt … --tools "" --no-session-persistence --setting-sources "" --strict-mcp-config --mcp-config '{"mcpServers":{}}' --exclude-dynamic-system-prompt-sections` (флаги изоляции проверены: без них в контекст попадают глобальный CLAUDE.md и MCP); без `--bare` (с подпиской не работает); запуск из `CLAUDE_CLI_WORKDIR` (пустая папка); таймаут и параллелизм из профиля; `structured_output`, `usage`, `modelUsage`, `total_cost_usd` из ответа; `subscription=true` в `llm_runs`. Тесты с подменой бинаря `testdata/claude`; живой тест `CLAUDE_CLI_LIVE=1`.
- `anthropic-api`: перед кодом подключить `/claude-api`; `anthropic-sdk-go`, structured output через `output_config.format`, `cache_control` на стабильном префиксе. `openai-api`: `openai-go/v3`, Responses API, `json_schema` strict.
- Две роли, два промпта, две схемы: интерпретатор (текст → намерение JSON) и рассказчик (события → хроника в группу, один вызов на раунд; приватные итоги исключены решением руководителя 2026-10-10). Рассказчик не меняет факты.
- Выход интерпретатора — массив намерений по `model.Intent` (`submission_id`, `action_type`, `target`, `approach_idx` либо `attribute`/`skill`/`risk`/`on_success`/`on_fail`, `push`, `ambiguities`, `fallback`), один вызов на все заявки раунда после дедлайна; валидация схемой и по снимку; невалидно → один повтор → безопасное толкование (`fallback`, иначе `wait`; G3). Сложность и бросок только у движка.
- Текст игрока — данные в выделенном блоке, никогда в системной части; инструкции из него не исполняются. Это же для `public_bio` и `secret_bio` персонажа; `secret_bio` в контексте рассказчика только как данные для подачи линии персонажа и в тексте не раскрывается.
- Контекст по видимости адресата отдельным компонентом с тестами на утечки.
- Каждый вызов в `llm_runs`: профиль, провайдер, модель, версия промпта, токены, `cost_usd`, признак подписки, длительность, ошибка, `game_id`, `round_id`.
- Бюджеты на вызов, раунд, игру; превышение → режим деградации (шаблоны), не падение раунда. Повтор вызова не меняет рассчитанные броски.
- Секреты только из конфига (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CLAUDE_CLI_PATH`), в БД не хранятся.
- Промпты `assets/prompts/<роль>/vN.md` через `embed`; версия в `llm_runs`.
