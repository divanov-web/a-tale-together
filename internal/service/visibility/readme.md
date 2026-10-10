# service/visibility
- Назначение: какие события доступны адресату: `public` всем, `location` присутствующим в локации события (`Viewer.LocationID`), `private` по `VisibleTo`. `Public(events)` — события для хроники (без приватных).
- Публичный вход: `Filter(events, Viewer)`, `Visible(event, Viewer)`, `Public(events)`.
- Правило: рассказчик и приватные итоги получают только отфильтрованные события; тесты на утечки в `filter_test.go`.
