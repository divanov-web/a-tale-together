-- +goose Up
-- Нейтральная фракция убрана из старта: мир начинается с двух сторон конфликта, остальные создаёт рассказчик по сюжету.
UPDATE genres SET template = jsonb_set(template, '{factions}',
  (SELECT COALESCE(jsonb_agg(f - 'relations' || jsonb_build_object('relations', (f->'relations') - 'neutral')), '[]'::jsonb)
   FROM jsonb_array_elements(template->'factions') f WHERE f->>'code' <> 'neutral'));
UPDATE genres SET template = jsonb_set(template, '{opening_scene}',
  to_jsonb(replace(template->>'opening_scene', 'а на рынке {{faction_neutral}} шепчутся', 'а на рынке шепчутся')));
UPDATE factions SET relations = relations - 'neutral';
DELETE FROM factions f WHERE f.code = 'neutral' AND NOT EXISTS (SELECT 1 FROM characters c WHERE c.faction_id = f.id);
-- +goose Down
-- Удалённые фракции игр и отношения не восстанавливаются; шаблон жанра fantasy получает нейтральную фракцию обратно.
UPDATE genres SET template = jsonb_set(template, '{factions}', template->'factions' ||
  '{"code": "neutral", "title": "Вольные", "description": "Торговцы и наёмники, выгода превыше флага.", "tone": "циничные посредники", "relations": {"good": "neutral", "evil": "neutral"}}'::jsonb)
WHERE code = 'fantasy';
