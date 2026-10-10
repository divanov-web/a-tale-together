# service/engine
- Назначение: детерминированное разрешение раунда (пункт 07): `Resolve(snapshot, intents, seed)` — фазы (помощь и ожидание → перемещение → взаимодействие → итоги), проверки пулом d6 из `rand.NewPCG(seed)`, форсирование, пресеты режимов, события с видимостью, ровно один статус на заявку (`resolved | rejected | deferred | ambiguous`); `Apply(snapshot, events)` — свёртка событий в состояние; `Clone` — глубокая копия снимка (движок исходные данные не меняет).
- Поддержано: `move`, `explore`, `interact` (подходы объектов: `state`, `object`+`state`, `reveal`, `fact`; `on_fail`: `condition`, `damage`), `wait`, `help` (+1 кубик цели, не более +2, пока без ограничения группой); `attack`, `persuade`, `sneak`, `trade` → `rejected` с пунктом плана. HP 0 → `character_died`; счётчик 0 у `dead` → `character_returned` (docs/game/death.md).
- Цель типовой команды сравнивается с названиями по основе слова (`matches`) — временно до интерпретатора (пункт 08).
- Правило: чистые функции без I/O и времени; новое правило — сначала сценарий в `docs/game/scenarios.md`, затем тест (`TestScenario_SNN_*`), затем код.
- Тесты: `engine_test.go` — S06, S07, S10–S12, помощь.
