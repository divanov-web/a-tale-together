-- +goose Up
-- Пол персонажа (B18): задаётся игроком при создании, пустое значение — не указан; существующие персонажи без пола.
ALTER TABLE characters ADD COLUMN gender text NOT NULL DEFAULT '' CHECK (gender IN ('', 'male', 'female'));

-- +goose Down
ALTER TABLE characters DROP COLUMN gender;
