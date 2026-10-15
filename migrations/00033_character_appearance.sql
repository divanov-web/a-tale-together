-- +goose Up
-- Описание внешности персонажа (B41): необязательное поле при создании, до 300 символов; рассказчику не передаётся.
ALTER TABLE characters ADD COLUMN appearance varchar(300) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE characters DROP COLUMN appearance;
