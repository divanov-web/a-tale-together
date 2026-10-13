-- +goose Up
-- Реплики персонажей на раунд (пункт 32, ADR-0027): отдельно от заявок, одна на персонажа в раунде; при пропуске раунда переносятся в следующий.
CREATE TABLE speeches (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id      bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id     bigint NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    character_id bigint NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    text         text NOT NULL,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (round_id, character_id)
);
CREATE INDEX speeches_game_idx ON speeches (game_id);

-- +goose Down
DROP TABLE speeches;
