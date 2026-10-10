-- +goose Up
-- Песочница (пункт 10, шаг 7): короткие раунды в dev. Продуктовый минимум 10 минут проверяет приложение (`game.Set`),
-- база хранит только общий диапазон.
ALTER TABLE games DROP CONSTRAINT games_round_minutes_check;
ALTER TABLE games ADD CONSTRAINT games_round_minutes_check CHECK (round_minutes BETWEEN 1 AND 60);

-- +goose Down
ALTER TABLE games DROP CONSTRAINT games_round_minutes_check;
ALTER TABLE games ADD CONSTRAINT games_round_minutes_check CHECK (round_minutes BETWEEN 10 AND 60);
