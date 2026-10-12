-- +goose Up
-- Пункт 28 (ADR-0020, docs/game/world.md «Квесты»): цели фракций (строка на версию), квесты с этапами, условия
-- этапа как данные из каталога (object_state, fact, visited; item_owned и npc_state — пункт 30), награда — факт.
-- Лимиты активных квестов на фракцию в настройках игры; шаблон жанра получает цель фракции и сюжетный квест
-- на случай сбоя модели.

CREATE TABLE quests (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id          bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    faction_id       bigint NOT NULL REFERENCES factions (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('story', 'side')),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'done')),
    title            text NOT NULL,
    summary          text NOT NULL DEFAULT '',
    created_round_id bigint REFERENCES rounds (id) ON DELETE SET NULL,
    done_round_id    bigint REFERENCES rounds (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX quests_game_idx ON quests (game_id);
CREATE INDEX quests_faction_idx ON quests (faction_id) WHERE status = 'active';

CREATE TABLE quest_stages (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id       bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    quest_id      bigint NOT NULL REFERENCES quests (id) ON DELETE CASCADE,
    stage_no      int NOT NULL,
    key           text NOT NULL,
    title         text NOT NULL,
    summary       text NOT NULL DEFAULT '',
    distance      int NOT NULL DEFAULT 1,
    condition     jsonb NOT NULL DEFAULT '{}'::jsonb,
    reward        jsonb NOT NULL DEFAULT '{}'::jsonb,
    location_id   bigint REFERENCES locations (id) ON DELETE SET NULL,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'bound', 'done')),
    done_round_id bigint REFERENCES rounds (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (quest_id, stage_no),
    UNIQUE (quest_id, key)
);
CREATE INDEX quest_stages_game_idx ON quest_stages (game_id);

CREATE TABLE faction_goals (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id    bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    faction_id bigint NOT NULL REFERENCES factions (id) ON DELETE CASCADE,
    version    int NOT NULL DEFAULT 1,
    text       text NOT NULL,
    stage      text NOT NULL DEFAULT '',
    quest_id   bigint REFERENCES quests (id) ON DELETE SET NULL,
    round_id   bigint REFERENCES rounds (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (faction_id, version)
);
CREATE INDEX faction_goals_game_idx ON faction_goals (game_id);

-- Лимиты активных квестов на фракцию (решение руководителя 2026-10-12): 2 сюжетных + 1 побочный.
UPDATE games SET settings = settings || '{"max_story_quests": 2, "max_side_quests": 1}'::jsonb
WHERE settings->>'max_story_quests' IS NULL;

-- Шаблон жанра: цель фракции по умолчанию и сюжетный квест на случай сбоя модели.
UPDATE genres SET template = jsonb_set(template, '{factions}', (
    SELECT jsonb_agg(
        CASE f->>'code'
            WHEN 'good' THEN f || '{"goal": "Найти пропавшего гонца и вернуть печать Ордена."}'::jsonb
            WHEN 'evil' THEN f || '{"goal": "Перехватить печать Ордена раньше Ордена."}'::jsonb
            ELSE f
        END ORDER BY ord)
    FROM jsonb_array_elements(template->'factions') WITH ORDINALITY AS t(f, ord)
)) WHERE code = 'fantasy';

UPDATE genres SET template = jsonb_set(template, '{story_quest}', $json$
{
  "title": "След гонца",
  "summary": "Гонец пропал по дороге к башне; его след ведёт через окрестности города.",
  "stages": [
    {"key": "trail", "title": "Найти след", "summary": "Отыскать место, где гонца видели в последний раз.", "distance": 1, "condition": "fact", "reward": "Гонец свернул с дороги не по своей воле."},
    {"key": "hideout", "title": "Выйти к укрытию", "summary": "Найти укрытие, куда увели гонца.", "distance": 2, "condition": "visited"},
    {"key": "seal", "title": "Вернуть печать", "summary": "Добыть печать Ордена из тайника похитителей.", "distance": 1, "condition": "object_state", "reward": "Печать Ордена снова у своих."}
  ]
}
$json$::jsonb) WHERE code = 'fantasy';

-- +goose Down
UPDATE genres SET template = template - 'story_quest' WHERE code = 'fantasy';
UPDATE genres SET template = jsonb_set(template, '{factions}', (
    SELECT jsonb_agg(f - 'goal' ORDER BY ord) FROM jsonb_array_elements(template->'factions') WITH ORDINALITY AS t(f, ord)
)) WHERE code = 'fantasy';
UPDATE games SET settings = settings - 'max_story_quests' - 'max_side_quests';
DROP TABLE faction_goals;
DROP TABLE quest_stages;
DROP TABLE quests;
