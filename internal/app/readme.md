# app
- Назначение: прикладные сценарии, связывающие сервисы и адаптеры: `bot` (команды и диалоги), `chat` (чаты и права), `game` (игра, участники, персонажи); позже `round`, `scheduler`, `delivery` (outbox).
- Правило: тонкие шаги без игровой логики и SQL; границы транзакций здесь (`InTx` через контекст, ADR-0008).
- Связи: [архитектура](../../docs/architecture/overview.md), [слои](../../docs/architecture/layers.md), [конвейер раунда](../../docs/architecture/round-pipeline.md).
