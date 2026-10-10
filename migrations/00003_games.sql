-- +goose Up
-- Пункт 03: справочники моделей персонажа и жанров, игры, сезоны, фракции, участники.
CREATE TABLE character_models (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    rules_version int NOT NULL DEFAULT 0,
    definition    jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE genres (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code               text NOT NULL UNIQUE,
    title              text NOT NULL,
    character_model_id bigint NOT NULL REFERENCES character_models (id),
    template           jsonb NOT NULL,
    enabled            boolean NOT NULL DEFAULT true,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE games (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    chat_id            bigint NOT NULL REFERENCES chats (id),
    genre_id           bigint NOT NULL REFERENCES genres (id),
    format             text NOT NULL DEFAULT 'novel',
    mode               text NOT NULL DEFAULT 'balanced',
    title              text NOT NULL,
    status             text NOT NULL DEFAULT 'draft',
    timezone           text NOT NULL DEFAULT 'Europe/Moscow',
    play_from          time,
    play_to            time,
    round_minutes      int NOT NULL DEFAULT 15 CHECK (round_minutes BETWEEN 10 AND 60),
    settings           jsonb NOT NULL DEFAULT '{}'::jsonb,
    rules_version      int NOT NULL DEFAULT 0,
    join_code          text NOT NULL UNIQUE,
    created_by_user_id bigint NOT NULL REFERENCES users (id),
    next_round_at      timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
-- В чате не более одной незавершённой игры (ADR-0007).
CREATE UNIQUE INDEX games_chat_unfinished_idx ON games (chat_id) WHERE status <> 'finished';

CREATE TABLE seasons (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id     bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    season_no   int NOT NULL,
    title       text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'active',
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    UNIQUE (game_id, season_no)
);

CREATE TABLE factions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id     bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    code        text NOT NULL,
    title       text NOT NULL,
    description text NOT NULL DEFAULT '',
    tone        text NOT NULL DEFAULT '',
    relations   jsonb NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (game_id, code)
);

CREATE TABLE game_members (
    game_id   bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    user_id   bigint NOT NULL REFERENCES users (id),
    role      text NOT NULL DEFAULT 'player',
    status    text NOT NULL DEFAULT 'active',
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, user_id)
);
CREATE INDEX game_members_user_idx ON game_members (user_id);

ALTER TABLE users ADD COLUMN current_game_id bigint REFERENCES games (id) ON DELETE SET NULL;

-- Базовая модель персонажа фэнтези (docs/game/models/character-basic.md).
INSERT INTO character_models (code, rules_version, definition) VALUES ('basic_fantasy', 0, $json$
{
  "schema_version": 1,
  "attributes": [
    {"key": "strength", "title": "Сила", "min": 2, "max": 4},
    {"key": "agility", "title": "Ловкость", "min": 2, "max": 4},
    {"key": "wits", "title": "Разум", "min": 2, "max": 4},
    {"key": "will", "title": "Воля", "min": 2, "max": 4}
  ],
  "attribute_points": 12,
  "skills": [
    {"key": "thievery", "title": "Воровство", "attribute": "agility"},
    {"key": "awareness", "title": "Внимательность", "attribute": "wits"},
    {"key": "persuasion", "title": "Убеждение", "attribute": "will"},
    {"key": "survival", "title": "Выживание", "attribute": "wits"},
    {"key": "stealth", "title": "Скрытность", "attribute": "agility"},
    {"key": "melee", "title": "Бой", "attribute": "strength"}
  ],
  "skill_points": 6,
  "skill_max": 3,
  "hp_formula": "4 + strength",
  "inventory_limit": 6,
  "archetypes": [
    {"key": "warrior", "title": "Воин", "attributes": {"strength": 4, "agility": 3, "wits": 2, "will": 3}, "skills": {"melee": 3, "survival": 2, "awareness": 1}, "items": ["sword", "shield"]},
    {"key": "rogue", "title": "Плут", "attributes": {"strength": 2, "agility": 4, "wits": 3, "will": 3}, "skills": {"thievery": 3, "stealth": 2, "awareness": 1}, "items": ["dagger", "lockpicks"]},
    {"key": "mage", "title": "Маг", "attributes": {"strength": 2, "agility": 2, "wits": 4, "will": 4}, "skills": {"awareness": 3, "persuasion": 2, "survival": 1}, "items": ["staff", "tome"]},
    {"key": "diplomat", "title": "Дипломат", "attributes": {"strength": 2, "agility": 3, "wits": 3, "will": 4}, "skills": {"persuasion": 3, "awareness": 2, "thievery": 1}, "items": ["seal", "coin_purse"]},
    {"key": "ranger", "title": "Следопыт", "attributes": {"strength": 3, "agility": 4, "wits": 3, "will": 2}, "skills": {"survival": 3, "stealth": 2, "melee": 1}, "items": ["bow", "rope"]}
  ]
}
$json$::jsonb);

-- Первый жанр: классическое фэнтези (G6; docs/game/models/genre.md).
INSERT INTO genres (code, title, character_model_id, template)
SELECT 'fantasy', 'Классическое фэнтези', id, $json$
{
  "schema_version": 1,
  "character_model": "basic_fantasy",
  "tone": "Маленький город на краю леса. Приключение, лёгкая ирония, без мрачности.",
  "factions": [
    {"code": "good", "title": "Орден Рассвета", "description": "Хранители города, верят в закон и свет.", "tone": "строгие, не безгрешные", "relations": {"evil": "hostile", "neutral": "neutral"}},
    {"code": "evil", "title": "Гильдия теней", "description": "Контрабандисты и шпионы; хотят свободы от Ордена.", "tone": "сторона конфликта, не злодеи", "relations": {"good": "hostile", "neutral": "neutral"}},
    {"code": "neutral", "title": "Вольные", "description": "Торговцы и наёмники, выгода превыше флага.", "tone": "циничные посредники", "relations": {"good": "neutral", "evil": "neutral"}}
  ],
  "opening_scene": "В {{world_title}} наступает осень. {{faction_good}} объявил награду за пропавшего гонца, {{faction_evil}} молчит, а на рынке {{faction_neutral}} шепчутся о свете в старой башне.",
  "items": [
    {"key": "sword", "title": "Меч", "tags": ["weapon", "melee"], "bonus": {"skill": "melee", "dice": 1}},
    {"key": "shield", "title": "Щит", "tags": ["armor"]},
    {"key": "dagger", "title": "Кинжал", "tags": ["weapon", "melee"]},
    {"key": "lockpicks", "title": "Отмычки", "tags": ["tool"], "bonus": {"skill": "thievery", "dice": 1}},
    {"key": "staff", "title": "Посох", "tags": ["weapon", "focus"]},
    {"key": "tome", "title": "Фолиант", "tags": ["book"], "bonus": {"skill": "awareness", "dice": 1}},
    {"key": "seal", "title": "Печать гильдии", "tags": ["token"], "bonus": {"skill": "persuasion", "dice": 1}},
    {"key": "coin_purse", "title": "Кошель", "tags": ["valuable"]},
    {"key": "bow", "title": "Лук", "tags": ["weapon", "ranged"]},
    {"key": "rope", "title": "Верёвка", "tags": ["tool"], "bonus": {"skill": "survival", "dice": 1}}
  ]
}
$json$::jsonb
FROM character_models WHERE code = 'basic_fantasy';

-- +goose Down
ALTER TABLE users DROP COLUMN current_game_id;
DROP TABLE game_members;
DROP TABLE factions;
DROP TABLE seasons;
DROP TABLE games;
DROP TABLE genres;
DROP TABLE character_models;
