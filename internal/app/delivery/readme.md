# app/delivery
- Назначение: воркер outbox (ADR-0006, ADR-0009): раз в интервал (`OUTBOX_TICK`, по умолчанию 3с) берёт ожидающие сообщения под `SKIP LOCKED` по порядку `id` (части длинной хроники идут подряд), отправляет в Telegram, хранит `telegram_message_id`; ошибка — повтор через 5с, 10с, 20с… до 5 попыток, затем `failed`; застрявшее `sending` дольше 5 минут → `unknown` без переотправки.
- Публичный вход: `New(Store, Sender, Clock, Settings, logger)`, `Service.Run(ctx)`, `Service.Deliver(ctx)` (один проход, для тестов).
- Интерфейсы потребителя: `Store` (`adapter/postgres`: `ClaimOutbox`, `FinishOutbox`), `Sender` (`adapter/telegram.Send`), `Clock`.
- Тесты: `service_test.go` на `adapter/postgres/fake`.
