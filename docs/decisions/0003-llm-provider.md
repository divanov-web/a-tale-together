# ADR-0003: Провайдер LLM через интерфейс и профили

- Статус: принято
- Дата: 2026-10-10
- Область: `internal/adapter/llm`, `internal/service/{interpreter,narrator}`, таблицы `llm_profiles`, `app_settings`, бюджет

## Контекст
Два вида вызовов (интерпретатор: строгий JSON; рассказчик: длинный текст). Руководитель хочет гибко выбирать источник ИИ: локальная подписка Claude Code без оплаты токенов, API Claude с кредитами подписки Max, API OpenAI; переключение из админки или БД; расширяемость.

## Решение
- Один интерфейс `llm.Client` у потребителей; реестр реализаций по ключу провайдера.
- Реализации: `claude-cli` (`claude -p --output-format json --json-schema`), `anthropic-api` (`anthropic-sdk-go`), `openai-api` (`openai-go`), `fake` (тесты).
- Выбор по профилю из БД: `games.llm_profile_id` → `app_settings.default_llm_profile`. Секреты только в окружении.
- Переключение командами владельца бота; веб-админка вне MVP.
- Рекомендация по умолчанию: локальный тестовый бот — `claude-cli`; прод-бот — `anthropic-api` на кредитах Max (условия подписки не предназначены для сервисов с чужими пользователями). Подтверждение: вопросы T1, T2.

Подробности: [providers.md](../llm/providers.md).

## Последствия
Правило `.claude/rules/llm.md`; таблицы `llm_profiles`, `app_settings`, поле `games.llm_profile_id`, `llm_runs.profile_id`; `.env.example` с ключами провайдеров; план 06 «Локальные токены» включает интерфейс, реестр, `claude-cli` и команды `/llm`.
