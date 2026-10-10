# app/scheduler
- Назначение: цикл планировщика: раз в интервал (`SCHEDULER_TICK`, по умолчанию 10с) вызывает `round.Tick(ctx, now)`; первый тик сразу при старте, ошибки тика в лог.
- Публичный вход: `New(Rounds, Clock, interval, logger)`, `Service.Run(ctx)` до отмены контекста.
- Интерфейсы потребителя: `Rounds` (`app/round`), `Clock`.
- Тесты: `service_test.go` (тики до отмены, ошибка не останавливает цикл).
