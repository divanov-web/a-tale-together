-- +goose Up
-- Подсказка рассказчика по просьбе фракции (пункт 35, ADR-0038): номер раунда, в котором фракция попросила подсказку
-- и ещё не получила её в хронике; NULL — просьбы нет. Снимается кодом после хроники с подсказкой.
ALTER TABLE factions ADD COLUMN hint_round int;

-- +goose Down
ALTER TABLE factions DROP COLUMN hint_round;
