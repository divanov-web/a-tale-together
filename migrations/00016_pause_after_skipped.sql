-- +goose Up
-- B11: автоматическая пауза после пропущенных без кворума раундов подряд. Настройка разработчика
-- `settings.pause_after_skipped` (3; 0 — выключено) и счётчик подряд пропущенных раундов у игры.
ALTER TABLE games ADD COLUMN skipped_streak int NOT NULL DEFAULT 0;
UPDATE games SET settings = settings || '{"pause_after_skipped": 3}'::jsonb WHERE NOT settings ? 'pause_after_skipped';

-- +goose Down
UPDATE games SET settings = settings - 'pause_after_skipped';
ALTER TABLE games DROP COLUMN skipped_streak;
