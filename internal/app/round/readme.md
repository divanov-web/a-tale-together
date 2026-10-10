# app/round
- Назначение: жизненный цикл раунда без разрешения (пункт 05, [ADR-0009](../../../docs/decisions/0009-round-scheduler.md)): `/begin` (стартовая сцена, первый раунд), открытие раундов по игровому окну (`window.go`), приём и замена заявок, заморозка со снимком и seed или пропуск без кворума с переносом заявок, статусы персонажей по пропускам, пауза, ручной шаг `/next`; игровые тексты (`texts.go`) только через outbox.
- Публичный вход: `New(Store, Rights, Clock, logger)`; поле `AnnounceSkipped` (сообщение о пропуске в группу, `cmd` включает в dev); `Begin`, `Pause`, `Next`, `Submit`, `Info`, `Tick(ctx, now)` для планировщика; `FormatTime`, `SkippedText`.
- Интерфейсы потребителя: `Store` (`adapter/postgres`: игры под `LockGame`, раунды, заявки, персонажи, outbox), `Rights` (`app/game.CanAdmin`), `Clock`.
- Ошибки для ответа игроку: `ErrForbidden`, `ErrAlreadyStarted`, `ErrNotActive`, `ErrPaused`, `ErrFinished`, `ErrNoCharacters`, `ErrNoOpenRound`, `ErrEmpty`, `ErrTooLong`, `ErrAlreadyActed`, `ErrTooFrequent` (замена только при `settings.allow_resubmit`).
- Правило: каждая игра обрабатывается в своей транзакции под блокировкой строки; состояние только в БД; `games.next_round_at` — единственный таймер; замороженный раунд остаётся `frozen` до конвейера разрешения (пункты 07–09).
- Тесты: сценарии на `adapter/postgres/fake` с управляемыми часами (`service_test.go`).
