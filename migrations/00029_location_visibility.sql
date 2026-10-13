-- +goose Up
-- Скрытые соседи (B36, ADR-0025): рассказчик в карточке соседа решает, виден ли он из занятой клетки; скрытый
-- известен игрокам только видом издалека (distant_view; пусто — «неизведанная территория») до первого входа.
-- Существующие клетки видимы.
ALTER TABLE locations
    ADD COLUMN hidden       boolean NOT NULL DEFAULT false,
    ADD COLUMN distant_view text    NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE locations DROP COLUMN hidden, DROP COLUMN distant_view;
