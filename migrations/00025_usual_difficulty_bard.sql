-- +goose Up
-- Решение руководителя 2026-10-12: обычная сложность 4 вместо 5 (риск normal, ориентир рассказчику, потолок
-- «повествования» — в коде `rules.RiskDifficulty`); подходы со сложностью 5 в шаблоне жанра становятся 4. Объекты
-- уже созданных клеток не трогаются: мир меняется только событиями, новые клетки рассказчик строит по новому
-- ориентиру. Сложность клетки (0–2) значения 5 не принимает, поэтому замена текстом задевает только подходы.
-- Архетип «Дипломат» заменён фэнтезийным «Бардом» с теми же числами; стартовый предмет «Печать гильдии» — «Лютня».

UPDATE genres SET template = replace(template::text, '"difficulty": 5', '"difficulty": 4')::jsonb
WHERE template::text LIKE '%"difficulty": 5%';

UPDATE character_models SET definition = jsonb_set(definition, '{archetypes}', (
    SELECT jsonb_agg(CASE WHEN a->>'key' = 'diplomat'
        THEN a || '{"key": "bard", "title": "Бард", "items": ["lute", "coin_purse"]}'::jsonb ELSE a END ORDER BY o)
    FROM jsonb_array_elements(definition->'archetypes') WITH ORDINALITY AS t(a, o)))
WHERE definition->'archetypes' @> '[{"key": "diplomat"}]';

UPDATE genres SET template = jsonb_set(template, '{items}', (
    SELECT jsonb_agg(CASE WHEN i->>'key' = 'seal'
        THEN '{"key": "lute", "title": "Лютня", "tags": ["instrument"]}'::jsonb ELSE i END ORDER BY o)
    FROM jsonb_array_elements(template->'items') WITH ORDINALITY AS t(i, o)))
WHERE template->'items' @> '[{"key": "seal"}]';

UPDATE characters SET archetype = 'bard',
    inventory = replace(replace(inventory::text, '"key": "seal"', '"key": "lute"'), '"Печать гильдии"', '"Лютня"')::jsonb
WHERE archetype = 'diplomat';

-- +goose Down
UPDATE characters SET archetype = 'diplomat',
    inventory = replace(replace(inventory::text, '"key": "lute"', '"key": "seal"'), '"Лютня"', '"Печать гильдии"')::jsonb
WHERE archetype = 'bard';

UPDATE genres SET template = jsonb_set(template, '{items}', (
    SELECT jsonb_agg(CASE WHEN i->>'key' = 'lute'
        THEN '{"key": "seal", "title": "Печать гильдии", "tags": ["token"]}'::jsonb ELSE i END ORDER BY o)
    FROM jsonb_array_elements(template->'items') WITH ORDINALITY AS t(i, o)))
WHERE template->'items' @> '[{"key": "lute"}]';

UPDATE character_models SET definition = jsonb_set(definition, '{archetypes}', (
    SELECT jsonb_agg(CASE WHEN a->>'key' = 'bard'
        THEN a || '{"key": "diplomat", "title": "Дипломат", "items": ["seal", "coin_purse"]}'::jsonb ELSE a END ORDER BY o)
    FROM jsonb_array_elements(definition->'archetypes') WITH ORDINALITY AS t(a, o)))
WHERE definition->'archetypes' @> '[{"key": "bard"}]';

UPDATE genres SET template = replace(template::text, '"difficulty": 4', '"difficulty": 5')::jsonb
WHERE template::text LIKE '%"difficulty": 4%';
