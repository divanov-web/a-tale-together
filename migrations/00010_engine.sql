-- +goose Up
-- Пункт 07: локации, журнал событий, итоги заявок, ненастоящая смерть и знания персонажа; стартовые локации жанра.
CREATE TABLE locations (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id     bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    difficulty  int NOT NULL DEFAULT 0 CHECK (difficulty BETWEEN 0 AND 2),
    links       jsonb NOT NULL DEFAULT '[]'::jsonb,
    objects     jsonb NOT NULL DEFAULT '[]'::jsonb,
    active      boolean NOT NULL DEFAULT true,
    start       boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, name)
);

CREATE TABLE world_events (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id      bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id     bigint NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    seq          bigint NOT NULL,
    type         text NOT NULL,
    payload      jsonb NOT NULL,
    visibility   text NOT NULL,
    visible_to   bigint[] NOT NULL DEFAULT '{}',
    location_id  bigint,
    character_id bigint,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, seq)
);
CREATE INDEX world_events_round_idx ON world_events (round_id);

CREATE TABLE resolved_actions (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id        bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id       bigint NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    submission_id  bigint NOT NULL UNIQUE REFERENCES action_submissions (id) ON DELETE CASCADE,
    character_id   bigint NOT NULL,
    intent         jsonb NOT NULL,
    status         text NOT NULL,
    outcome        text NOT NULL DEFAULT '',
    reason         text NOT NULL DEFAULT '',
    summary        text NOT NULL DEFAULT '',
    checks         jsonb,
    event_seqs     bigint[] NOT NULL DEFAULT '{}',
    conflict_group text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resolved_actions_round_idx ON resolved_actions (round_id);
CREATE INDEX resolved_actions_game_idx ON resolved_actions (game_id);

ALTER TABLE characters
    ADD COLUMN death_rounds_left int NOT NULL DEFAULT 0,
    ADD COLUMN died_at timestamptz,
    ADD COLUMN knowledge jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Режим смерти и срок выбытия в настройках существующих игр (docs/game/death.md).
UPDATE games SET settings = settings || '{"death_mode": "fake", "death_days": 10}'::jsonb WHERE NOT settings ? 'death_mode';

-- Стартовые локации жанра фэнтези (docs/game/models/location.md).
UPDATE genres SET template = jsonb_set(template, '{locations}', $json$
[
  {"name": "Рыночная площадь", "description": "Сердце города: лавки, слухи и объявление о награде за пропавшего гонца.", "difficulty": 0, "start": true,
   "links": [{"to": "Трактир «Три свечи»", "title": "в трактир"}, {"to": "Зал Серой башни", "title": "к башне"}],
   "objects": [
     {"key": "notice_board", "title": "доска объявлений", "description": "Награда за гонца, объявление Ордена; снизу приписка углём.",
      "approaches": [{"action": "explore", "title": "прочитать приписку", "attribute": "wits", "skill": "awareness", "difficulty": 1, "on_success": {"fact": "Приписка углём: «гонца видели у башни в полночь»."}}]},
     {"key": "well", "title": "старый колодец", "description": "Колодец с воротом; цепь обрывается в темноту.",
      "approaches": [{"action": "explore", "title": "заглянуть в колодец", "attribute": "agility", "skill": "awareness", "difficulty": 2, "on_success": {"fact": "На дне колодца блестит что-то металлическое: кольцо с печатью Ордена."}, "on_fail": {"condition": "exhausted"}}]}
   ]},
  {"name": "Трактир «Три свечи»", "description": "Тёплый зал, запах похлёбки, завсегдатаи у очага и хозяйка Марта за стойкой.", "difficulty": 0,
   "links": [{"to": "Рыночная площадь", "title": "на площадь"}],
   "objects": [
     {"key": "regulars", "title": "завсегдатаи у очага", "description": "Трое стариков обсуждают свет в башне.",
      "approaches": [{"action": "explore", "title": "послушать разговор", "attribute": "wits", "skill": "awareness", "difficulty": 1, "on_success": {"fact": "Старики шепчут: свет в башне появляется после полуночи, а дверь в подвал заперта с весны."}}]},
     {"key": "cellar_hatch", "title": "люк в погреб", "description": "Люк за стойкой, закрыт на засов.", "state": "closed",
      "approaches": [{"action": "interact", "title": "отодвинуть засов", "attribute": "strength", "difficulty": 1, "on_success": {"state": "open", "fact": "В погребе бочки и ящик с печатью Гильдии теней."}}]}
   ]},
  {"name": "Зал Серой башни", "description": "Пыльный зал с обвалившейся лестницей; вниз ведёт узкий ход в подвал.", "difficulty": 0,
   "links": [{"to": "Рыночная площадь", "title": "на площадь"}, {"to": "Подвал башни", "title": "вниз в подвал"}],
   "objects": [
     {"key": "statue", "title": "статуя рыцаря", "description": "Каменный рыцарь со сколотым щитом.",
      "approaches": [{"action": "explore", "title": "осмотреть статую", "attribute": "wits", "skill": "awareness", "difficulty": 2, "on_success": {"fact": "На щите статуи выцарапана карта: крест под подвалом башни."}}]}
   ]},
  {"name": "Подвал башни", "description": "Сырой подвал, запертая дверь в глубине и следы сапог на пыли.", "difficulty": 1,
   "links": [{"to": "Зал Серой башни", "title": "наверх в зал"}],
   "objects": [
     {"key": "cellar_door", "title": "запертая дверь", "description": "Тяжёлая дверь с новым замком.", "state": "locked",
      "approaches": [
        {"action": "interact", "title": "выбить", "attribute": "strength", "difficulty": 2, "on_success": {"state": "broken"}, "on_fail": {"condition": "exhausted"}},
        {"action": "interact", "title": "отпереть", "attribute": "agility", "skill": "thievery", "difficulty": 1, "on_success": {"state": "open"}},
        {"action": "explore", "title": "найти потайной рычаг", "attribute": "wits", "skill": "awareness", "difficulty": 3, "on_success": {"reveal": "hidden_lever"}}
      ]},
     {"key": "hidden_lever", "title": "потайной рычаг", "description": "Рычаг в нише за камнем.", "hidden_until_revealed": true,
      "approaches": [{"action": "interact", "title": "дёрнуть рычаг", "difficulty": 0, "on_success": {"object": "cellar_door", "state": "open"}}]}
   ]}
]
$json$::jsonb) WHERE code = 'fantasy';

-- Локации для уже созданных игр из шаблона их жанра; персонажи без локации — в стартовую.
INSERT INTO locations (game_id, name, description, difficulty, links, objects, start)
SELECT g.id, l->>'name', coalesce(l->>'description', ''), coalesce((l->>'difficulty')::int, 0), coalesce(l->'links', '[]'::jsonb), coalesce(l->'objects', '[]'::jsonb), coalesce((l->>'start')::boolean, false)
FROM games g JOIN genres ge ON ge.id = g.genre_id, jsonb_array_elements(coalesce(ge.template->'locations', '[]'::jsonb)) l
WHERE NOT EXISTS (SELECT 1 FROM locations x WHERE x.game_id = g.id);
UPDATE characters c SET location_id = l.id FROM locations l WHERE l.game_id = c.game_id AND l.start AND c.location_id IS NULL;

-- Бонусы предметов из каталога жанра в инвентарь существующих персонажей.
UPDATE characters c SET inventory = sub.inv
FROM (
  SELECT c2.id, jsonb_agg(CASE WHEN it->'bonus' IS NOT NULL THEN i || jsonb_build_object('bonus', it->'bonus') ELSE i END ORDER BY ord) AS inv
  FROM characters c2 JOIN games g ON g.id = c2.game_id JOIN genres ge ON ge.id = g.genre_id,
       jsonb_array_elements(c2.inventory) WITH ORDINALITY AS inv(i, ord)
       LEFT JOIN LATERAL (SELECT it FROM jsonb_array_elements(ge.template->'items') it WHERE it->>'key' = i->>'key' LIMIT 1) t ON true
  GROUP BY c2.id
) sub WHERE sub.id = c.id;

-- +goose Down
UPDATE characters SET inventory = (SELECT coalesce(jsonb_agg(i - 'bonus'), '[]'::jsonb) FROM jsonb_array_elements(inventory) i);
UPDATE genres SET template = template - 'locations' WHERE code = 'fantasy';
UPDATE games SET settings = settings - 'death_mode' - 'death_days';
ALTER TABLE characters DROP COLUMN knowledge, DROP COLUMN died_at, DROP COLUMN death_rounds_left;
DROP TABLE resolved_actions;
DROP TABLE world_events;
UPDATE characters SET location_id = NULL;
DROP TABLE locations;
