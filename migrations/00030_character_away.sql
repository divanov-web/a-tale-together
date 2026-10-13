-- +goose Up
-- Уход персонажа по слову игрока (пункт 20, ADR-0026): пометка `/away`; статус считает заморозка раунда.
ALTER TABLE characters ADD COLUMN away boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE characters DROP COLUMN away;
