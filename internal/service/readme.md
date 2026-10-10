# service
- Назначение: игровая логика: `rules` (модель персонажа, испытания, каталог, типовые команды), `engine` (разрешение раунда и свёртка событий), `visibility` (события по адресату); далее `interpreter`, `narrator`, `context`.
- Правило: не импортирует adapter, app, Telegram и SQL; движок детерминирован.
- Связи: [правила игры](../../docs/game/README.md), [LLM](../../docs/llm/README.md), `.claude/rules/engine.md`, `llm.md`.
