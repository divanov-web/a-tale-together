-- +goose Up
-- Логово сил зла (B42, ADR-0031): координаты опорной клетки фракции `evil`, одна из `(±3, ±3)`, выбирается кодом
-- при `/create`; NULL — логова нет (фракция `good`, фракции по сюжету, игры, созданные до миграции).
ALTER TABLE factions ADD COLUMN lair_x int, ADD COLUMN lair_y int;

-- +goose Down
ALTER TABLE factions DROP COLUMN lair_x, DROP COLUMN lair_y;
