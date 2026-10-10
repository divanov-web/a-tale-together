-- +goose Up
-- Пункт 04: персонажи и значения их характеристик.
CREATE TABLE characters (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id       bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    season_id     bigint NOT NULL REFERENCES seasons (id),
    user_id       bigint NOT NULL REFERENCES users (id),
    model_id      bigint NOT NULL REFERENCES character_models (id),
    nickname      text NOT NULL,
    archetype     text NOT NULL,
    public_bio    varchar(100) NOT NULL DEFAULT '',
    secret_bio    varchar(100) NOT NULL DEFAULT '',
    status        text NOT NULL DEFAULT 'active',
    missed_rounds int NOT NULL DEFAULT 0,
    faction_id    bigint NOT NULL REFERENCES factions (id),
    party_id      bigint,
    hp            int NOT NULL,
    conditions    jsonb NOT NULL DEFAULT '[]'::jsonb,
    inventory     jsonb NOT NULL DEFAULT '[]'::jsonb,
    location_id   bigint,
    xp            int NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (season_id, user_id),
    FOREIGN KEY (game_id, user_id) REFERENCES game_members (game_id, user_id)
);
CREATE INDEX characters_game_idx ON characters (game_id);
CREATE UNIQUE INDEX characters_season_nickname_idx ON characters (season_id, lower(nickname));

CREATE TABLE character_stats (
    game_id      bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    character_id bigint NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    kind         text NOT NULL,
    key          text NOT NULL,
    value        int NOT NULL,
    PRIMARY KEY (character_id, kind, key)
);
CREATE INDEX character_stats_game_idx ON character_stats (game_id);

-- +goose Down
DROP TABLE character_stats;
DROP TABLE characters;
