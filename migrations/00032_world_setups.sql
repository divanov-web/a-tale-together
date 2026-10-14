-- +goose Up
-- Варианты стартового мира от нулевого вызова «генерация мира» при /create и выбор администратора (ADR-0028).
-- Игры в `setup` с `next_round_at` попадают в тик планировщика: он делает вызов и присылает варианты администратору.
ALTER TABLE games ADD COLUMN world_setups jsonb;

-- +goose Down
ALTER TABLE games DROP COLUMN world_setups;
