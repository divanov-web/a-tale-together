-- +goose Up
-- Статус игры `setup` до завершения настройки администратором (B32, ADR-0023): создание персонажей закрыто,
-- завершение диалога настройки переводит в `draft`. Существующие игры статус не меняют.
ALTER TABLE games ADD CONSTRAINT games_status_check CHECK (status IN ('setup', 'draft', 'active', 'paused', 'finished'));

-- +goose Down
ALTER TABLE games DROP CONSTRAINT games_status_check;
