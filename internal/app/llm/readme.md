# app/llm
- Назначение: профили провайдеров LLM и единый вход для вызовов (пункт 06, [ADR-0003](../../../docs/decisions/0003-llm-provider.md), [providers.md](../../../docs/llm/providers.md)): выбор профиля игра → `app_settings.default_llm_profile`, кэш профилей в памяти (сброс при `Use`, `Reset`, `Add`), карта провайдеров по ключу, подстановка модели по роли и параметров профиля, запись каждого вызова в `llm_runs` (и при ошибке), проверка профиля `/llm test`.
- Публичный вход: `New(Store, map[string]Client, Clock, logger)`; `List`, `Profile`, `Resolve(ctx, *game)`, `Use(ctx, name, *gameID)`, `Reset`, `Add`, `Complete(ctx, Call{Game, Round, Role, PromptVersion}, model.LLMRequest)`, `Test(ctx, name)`, `Providers`.
- Интерфейсы потребителя: `Client` (`adapter/llm/*`), `Store` (`adapter/postgres`, в том числе `GetChat` для адресации журнала), `Clock`, `Journal` (поле; вход и выход каждого вызова с игрой в журнал группы игры, ADR-0012; вызовы без игры не пишутся).
- Ошибки для ответа владельцу: `ErrNoProfile`, `ErrUnknownProfile`, `ErrUnknownProvider`, `ErrProfileExists`, `ErrBadValue`, `ErrNoGame`.
- Правило: интерпретатор и рассказчик (пункты 08–09) вызывают модель только через `Complete`; секреты провайдеров в конфиге, не в профиле.
- Тесты: `service_test.go` на фейковом хранилище и провайдере `adapter/llm/fake`.
