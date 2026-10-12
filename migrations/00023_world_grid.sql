-- +goose Up
-- Пункт 27 (ADR-0019, docs/game/world.md): локация становится клеткой сетки с координатами, стадией и типом местности;
-- выходы считает код по соседям, поэтому links и active уходят; имя — подпись, не ключ. Объекты переезжают в
-- world_objects; таблицы items и npcs заложены для пункта 30. Открытия клеток по фракциям и счётчик исследования —
-- для выдачи квестов (пункт 28). generation_jobs — сохранённые ответы модели по ключу задания: пишутся вне транзакции
-- раунда, без внешних ключей (FOR UPDATE на games в транзакции раунда заблокировал бы проверку ключа).

ALTER TABLE locations
    ADD COLUMN x          int NOT NULL DEFAULT 0,
    ADD COLUMN y          int NOT NULL DEFAULT 0,
    ADD COLUMN stage      text NOT NULL DEFAULT 'detailed' CHECK (stage IN ('card', 'detailed')),
    ADD COLUMN terrain    text NOT NULL DEFAULT '',
    ADD COLUMN visited_at timestamptz;

-- Существующие локации по играм: стартовая в (0,0), остальные по кольцу в порядке создания.
-- +goose StatementBegin
DO $$
DECLARE
    g bigint;
    l RECORD;
    offsets int[][] := ARRAY[[0,1],[1,0],[0,-1],[-1,0],[1,1],[-1,1],[1,-1],[-1,-1],[0,2],[2,0],[0,-2],[-2,0]];
    i int;
BEGIN
    FOR g IN SELECT DISTINCT game_id FROM locations LOOP
        i := 0;
        FOR l IN SELECT id FROM locations WHERE game_id = g ORDER BY start DESC, id LOOP
            IF i = 0 THEN
                UPDATE locations SET x = 0, y = 0, start = true WHERE id = l.id;
            ELSE
                UPDATE locations SET x = offsets[i][1], y = offsets[i][2] WHERE id = l.id;
            END IF;
            i := i + 1;
        END LOOP;
    END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE locations DROP CONSTRAINT locations_game_id_name_key;
ALTER TABLE locations ADD CONSTRAINT locations_game_xy_key UNIQUE (game_id, x, y);
ALTER TABLE locations DROP COLUMN links, DROP COLUMN active;

-- Посещение: клетки, где сейчас стоят персонажи, и стартовая.
UPDATE locations l SET visited_at = now()
WHERE l.start OR EXISTS (SELECT 1 FROM characters c WHERE c.game_id = l.game_id AND c.location_id = l.id);

-- Объекты клеток в отдельной таблице; ключ уникален внутри клетки.
CREATE TABLE world_objects (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id               bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    location_id           bigint NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    key                   text NOT NULL,
    title                 text NOT NULL,
    description           text NOT NULL DEFAULT '',
    state                 text NOT NULL DEFAULT '',
    revealed              boolean NOT NULL DEFAULT false,
    hidden_until_revealed boolean NOT NULL DEFAULT false,
    searched              boolean NOT NULL DEFAULT false,
    approaches            jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (location_id, key)
);
CREATE INDEX world_objects_game_idx ON world_objects (game_id);

INSERT INTO world_objects (game_id, location_id, key, title, description, state, revealed, hidden_until_revealed, approaches)
SELECT l.game_id, l.id, o->>'key', coalesce(o->>'title', o->>'key'), coalesce(o->>'description', ''), coalesce(o->>'state', ''),
       coalesce((o->>'revealed')::boolean, false), coalesce((o->>'hidden_until_revealed')::boolean, false), coalesce(o->'approaches', '[]'::jsonb)
FROM locations l, jsonb_array_elements(l.objects) WITH ORDINALITY AS obj(o, ord)
WHERE o->>'key' IS NOT NULL
ORDER BY l.id, ord;
ALTER TABLE locations DROP COLUMN objects;

-- Переносимые предметы и NPC: таблицы заложены сразу (решение руководителя 2026-10-12), поведение — пункт 30.
CREATE TABLE items (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id      bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    key          text NOT NULL,
    title        text NOT NULL,
    kind         text NOT NULL DEFAULT '',
    props        jsonb NOT NULL DEFAULT '{}'::jsonb,
    location_id  bigint REFERENCES locations (id) ON DELETE CASCADE,
    object_id    bigint REFERENCES world_objects (id) ON DELETE CASCADE,
    character_id bigint REFERENCES characters (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, key),
    CHECK ((location_id IS NOT NULL)::int + (object_id IS NOT NULL)::int + (character_id IS NOT NULL)::int = 1)
);
CREATE INDEX items_game_idx ON items (game_id);

CREATE TABLE npcs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id     bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    location_id bigint REFERENCES locations (id) ON DELETE SET NULL,
    key         text NOT NULL,
    title       text NOT NULL,
    faction_id  bigint REFERENCES factions (id) ON DELETE SET NULL,
    state       text NOT NULL DEFAULT '',
    attitude    jsonb NOT NULL DEFAULT '{}'::jsonb,
    knowledge   jsonb NOT NULL DEFAULT '[]'::jsonb,
    stats       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, key)
);
CREATE INDEX npcs_game_idx ON npcs (game_id);

-- Открытия клеток по фракциям и счётчик исследования фракции.
CREATE TABLE location_discoveries (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id      bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    location_id  bigint NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    faction_id   bigint NOT NULL REFERENCES factions (id) ON DELETE CASCADE,
    round_id     bigint REFERENCES rounds (id) ON DELETE SET NULL,
    character_id bigint,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (location_id, faction_id)
);
CREATE INDEX location_discoveries_game_idx ON location_discoveries (game_id);
ALTER TABLE factions ADD COLUMN explored_since_reset int NOT NULL DEFAULT 0;

-- Уже занятые клетки считаются открытыми фракциями присутствующих персонажей без зачёта в счётчик.
INSERT INTO location_discoveries (game_id, location_id, faction_id, character_id)
SELECT DISTINCT ON (c.location_id, c.faction_id) c.game_id, c.location_id, c.faction_id, c.id
FROM characters c JOIN locations l ON l.id = c.location_id
WHERE c.location_id IS NOT NULL AND NOT l.start
ORDER BY c.location_id, c.faction_id, c.id;

-- Сохранённые ответы модели по ключу задания (вне транзакции раунда, без внешних ключей).
CREATE TABLE generation_jobs (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id    bigint NOT NULL,
    round_id   bigint,
    role       text NOT NULL,
    input_hash text NOT NULL,
    response   jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX generation_jobs_key_idx ON generation_jobs (game_id, coalesce(round_id, 0), role, input_hash);

-- Инвариант сетки: у каждой детализированной клетки есть четыре соседа хотя бы в стадии карточки.
INSERT INTO locations (game_id, name, description, difficulty, x, y, stage, terrain)
SELECT DISTINCT ON (l.game_id, l.x + d.dx, l.y + d.dy) l.game_id, 'Неизведанные земли', 'Что там, отсюда не разглядеть.', 0, l.x + d.dx, l.y + d.dy, 'card', 'unknown'
FROM locations l CROSS JOIN (VALUES (0, 1), (0, -1), (-1, 0), (1, 0)) AS d(dx, dy)
WHERE l.stage = 'detailed'
  AND NOT EXISTS (SELECT 1 FROM locations n WHERE n.game_id = l.game_id AND n.x = l.x + d.dx AND n.y = l.y + d.dy)
ORDER BY l.game_id, l.x + d.dx, l.y + d.dy;

-- Шаблон жанра: только стартовая клетка; остальной мир генерирует рассказчик.
UPDATE genres SET template = jsonb_set(template, '{locations}', $json$
[
  {"name": "Рыночная площадь", "description": "Сердце города: лавки, слухи и объявление о награде за пропавшего гонца.", "difficulty": 0, "start": true, "terrain": "town",
   "objects": [
     {"key": "notice_board", "title": "доска объявлений", "description": "Награда за гонца, объявление Ордена; снизу почти стёртая приписка углём.",
      "approaches": [{"action": "explore", "title": "разобрать стёртую приписку", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "Приписка углём: «гонца видели у башни в полночь»."}}]},
     {"key": "well", "title": "старый колодец", "description": "Колодец с воротом; цепь обрывается в темноту, у края натоптано.",
      "approaches": [
        {"action": "explore", "title": "заметить предмет на дне колодца", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "На дне колодца блестит что-то металлическое: кольцо с печатью Ордена."}, "on_fail": {"condition": "exhausted"}},
        {"action": "explore", "title": "распознать следы у колодца", "attribute": "wits", "skill": "survival", "difficulty": 5, "on_success": {"fact": "Следы у колодца: тяжёлые сапоги с подковками уходят в сторону башни, один из шедших хромал."}}
      ]}
   ]}
]
$json$::jsonb) WHERE code = 'fantasy';

-- +goose Down
DROP TABLE generation_jobs;
ALTER TABLE factions DROP COLUMN explored_since_reset;
DROP TABLE location_discoveries;
DROP TABLE npcs;
DROP TABLE items;
ALTER TABLE locations ADD COLUMN objects jsonb NOT NULL DEFAULT '[]'::jsonb;
UPDATE locations l SET objects = coalesce((
    SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object('key', o.key, 'title', o.title, 'description', o.description, 'state', nullif(o.state, ''),
        'revealed', CASE WHEN o.revealed THEN true END, 'hidden_until_revealed', CASE WHEN o.hidden_until_revealed THEN true END, 'approaches', o.approaches)) ORDER BY o.id)
    FROM world_objects o WHERE o.location_id = l.id), '[]'::jsonb);
DROP TABLE world_objects;
DELETE FROM locations WHERE stage = 'card';
ALTER TABLE locations ADD COLUMN links jsonb NOT NULL DEFAULT '[]'::jsonb, ADD COLUMN active boolean NOT NULL DEFAULT true;
ALTER TABLE locations DROP CONSTRAINT locations_game_xy_key;
ALTER TABLE locations DROP COLUMN x, DROP COLUMN y, DROP COLUMN stage, DROP COLUMN terrain, DROP COLUMN visited_at;
