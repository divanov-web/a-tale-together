# adapter
- Назначение: внешние системы: `postgres`, `telegram`, `llm`. Реализуют интерфейсы потребителей.
- Правило: каждый адаптер имеет фейк для тестов; все запросы к игровым данным с `game_id`.
- Связи: `.claude/rules/postgres.md`, `telegram.md`, `llm.md`; [ADR](../../docs/decisions/README.md).
