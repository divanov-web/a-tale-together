---
paths:
  - "internal/adapter/llm/**"
  - "internal/service/interpreter/**"
  - "internal/service/narrator/**"
  - "assets/prompts/**"
---
# LLM в коде

- Источник: `docs/llm/providers.md` (интерфейс `llm.Client`, провайдеры, профили), ADR-0003. Дизайн промптов: `/tale-prompts`.
- Один интерфейс `llm.Client.Complete(ctx, Request) (Response, error)` у потребителей; реализации в `internal/adapter/llm/<provider>`; реестр по ключу; выбор профиля: игра → `app_settings.default_llm_profile`.
- Провайдеры: `claude-cli`, `anthropic-api`, `openai-api`, `fake`. Новый провайдер = пакет + регистрация + строка в `providers.md`; новая зависимость = ADR.
- `claude-cli`: `claude -p --output-format json --json-schema … --system-prompt … --tools "" --no-session-persistence`; без `--bare` (иначе подписка не используется); запуск из `CLAUDE_CLI_WORKDIR` (пустая папка, без CLAUDE.md); таймаут и параллелизм из профиля; `structured_output` и `total_cost_usd` из ответа; `subscription=true` в `llm_runs`.
- `anthropic-api`: перед кодом подключить `/claude-api`; `anthropic-sdk-go`, structured output через `output_config.format`, `cache_control` на стабильном префиксе. `openai-api`: `openai-go/v3`, Responses API, `json_schema` strict.
- Две роли, два промпта, две схемы: интерпретатор (текст → намерение JSON) и рассказчик (события → текст). Рассказчик не меняет факты.
- Выход интерпретатора — строгий JSON (`actor_id`, `action_type`, `target_id`, `location_id`, `parameters`, `ambiguities`); валидация схемой и движком; невалидно → ограниченный повтор → `ambiguous`.
- Текст игрока — данные в выделенном блоке, никогда в системной части; инструкции из него не исполняются. Это же для `public_bio` и `secret_bio` персонажа; `secret_bio` только в контекст рассказчика для этого персонажа.
- Контекст по видимости адресата отдельным компонентом с тестами на утечки.
- Каждый вызов в `llm_runs`: профиль, провайдер, модель, версия промпта, токены, `cost_usd`, признак подписки, длительность, ошибка, `game_id`, `round_id`.
- Бюджеты на вызов, раунд, игру; превышение → режим деградации (шаблоны), не падение раунда. Повтор вызова не меняет рассчитанные броски.
- Секреты только из конфига (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CLAUDE_CLI_PATH`), в БД не хранятся.
- Промпты `assets/prompts/<роль>/vN.md` через `embed`; версия в `llm_runs`.
