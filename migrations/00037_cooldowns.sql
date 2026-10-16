-- +goose Up
-- Перезарядки действий (пункт 34, ADR-0033): последний раунд, в котором владелец (персонаж или фракция) выполнил
-- действие с ограничением частоты; длительности задают правила игры (service/rules). Действия: bio, secret, rename
-- у персонажа; hint у фракции (пункт 35).
CREATE TABLE cooldowns (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id    bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    owner_kind text NOT NULL CHECK (owner_kind IN ('character', 'faction')),
    owner_id   bigint NOT NULL,
    action     text NOT NULL,
    last_round int NOT NULL CHECK (last_round >= 1),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, owner_kind, owner_id, action)
);

-- +goose Down
DROP TABLE cooldowns;
