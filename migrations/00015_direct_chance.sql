-- +goose Up
-- Пункт 22, ADR-0014: система проверок «Прямой шанс» (rules_version 1). Один алгоритм в движке: старые игры
-- переводятся на версию 1 вместе со схемой; воспроизведение версии 0 не поддерживается.

ALTER TABLE games ALTER COLUMN rules_version SET DEFAULT 1;
ALTER TABLE character_models ALTER COLUMN rules_version SET DEFAULT 1;
UPDATE games SET rules_version = 1;

-- Модель персонажа версии 1 (docs/game/models/character-basic.md): характеристики 2–5 с суммой 14, атлетика,
-- билды с калибровкой 80 % / 20 %. Персонажи, созданные раньше, сохраняют свои значения; навык без записи равен 0.
UPDATE character_models SET rules_version = 1, definition = $json$
{
  "schema_version": 1,
  "attributes": [
    {"key": "strength", "title": "Сила", "min": 2, "max": 5},
    {"key": "agility", "title": "Ловкость", "min": 2, "max": 5},
    {"key": "wits", "title": "Разум", "min": 2, "max": 5},
    {"key": "will", "title": "Воля", "min": 2, "max": 5}
  ],
  "attribute_points": 14,
  "skills": [
    {"key": "athletics", "title": "Атлетика", "attribute": "strength"},
    {"key": "melee", "title": "Бой", "attribute": "strength"},
    {"key": "thievery", "title": "Воровство", "attribute": "agility"},
    {"key": "stealth", "title": "Скрытность", "attribute": "agility"},
    {"key": "awareness", "title": "Внимательность", "attribute": "wits"},
    {"key": "survival", "title": "Выживание", "attribute": "wits"},
    {"key": "persuasion", "title": "Убеждение", "attribute": "will"}
  ],
  "skill_points": 6,
  "skill_max": 3,
  "hp_formula": "4 + strength",
  "inventory_limit": 6,
  "archetypes": [
    {"key": "warrior", "title": "Воин", "attributes": {"strength": 5, "agility": 3, "wits": 2, "will": 4}, "skills": {"athletics": 3, "melee": 3}, "items": ["sword", "shield"]},
    {"key": "rogue", "title": "Плут", "attributes": {"strength": 2, "agility": 5, "wits": 4, "will": 3}, "skills": {"thievery": 3, "stealth": 3}, "items": ["dagger", "lockpicks"]},
    {"key": "mage", "title": "Маг", "attributes": {"strength": 2, "agility": 3, "wits": 5, "will": 4}, "skills": {"awareness": 3, "persuasion": 2, "survival": 1}, "items": ["staff", "tome"]},
    {"key": "diplomat", "title": "Дипломат", "attributes": {"strength": 3, "agility": 2, "wits": 4, "will": 5}, "skills": {"persuasion": 3, "awareness": 2, "survival": 1}, "items": ["seal", "coin_purse"]},
    {"key": "ranger", "title": "Следопыт", "attributes": {"strength": 4, "agility": 3, "wits": 5, "will": 2}, "skills": {"survival": 3, "stealth": 2, "melee": 1}, "items": ["bow", "rope"]}
  ]
}
$json$::jsonb WHERE code = 'basic_fantasy';

-- Стартовые предметы без бонуса к проверкам: в каталоге жанра и в инвентаре персонажей.
UPDATE genres SET template = jsonb_set(template, '{items}', (SELECT coalesce(jsonb_agg(i - 'bonus'), '[]'::jsonb) FROM jsonb_array_elements(template->'items') i))
WHERE template ? 'items';
UPDATE characters SET inventory = (SELECT coalesce(jsonb_agg(i - 'bonus'), '[]'::jsonb) FROM jsonb_array_elements(inventory) i)
WHERE inventory <> '[]'::jsonb;

-- Стартовый мир фэнтези (docs/game/checks.md, «Предлагаемые действия»): одиннадцать подходов с итоговой сложностью 5
-- (подвал: база 4 + локация 1), подходы следопыта и дипломата с фактами в данных.
UPDATE genres SET template = jsonb_set(template, '{locations}', $json$
[
  {"name": "Рыночная площадь", "description": "Сердце города: лавки, слухи и объявление о награде за пропавшего гонца.", "difficulty": 0, "start": true,
   "links": [{"to": "Трактир «Три свечи»", "title": "в трактир"}, {"to": "Зал Серой башни", "title": "к башне"}],
   "objects": [
     {"key": "notice_board", "title": "доска объявлений", "description": "Награда за гонца, объявление Ордена; снизу почти стёртая приписка углём.",
      "approaches": [{"action": "explore", "title": "разобрать стёртую приписку", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "Приписка углём: «гонца видели у башни в полночь»."}}]},
     {"key": "well", "title": "старый колодец", "description": "Колодец с воротом; цепь обрывается в темноту, у края натоптано.",
      "approaches": [
        {"action": "explore", "title": "заметить предмет на дне колодца", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "На дне колодца блестит что-то металлическое: кольцо с печатью Ордена."}, "on_fail": {"condition": "exhausted"}},
        {"action": "explore", "title": "распознать следы у колодца", "attribute": "wits", "skill": "survival", "difficulty": 5, "on_success": {"fact": "Следы у колодца: тяжёлые сапоги с подковками уходят в сторону башни, один из шедших хромал."}}
      ]}
   ]},
  {"name": "Трактир «Три свечи»", "description": "Тёплый зал, запах похлёбки, завсегдатаи у очага и хозяйка Марта за стойкой.", "difficulty": 0,
   "links": [{"to": "Рыночная площадь", "title": "на площадь"}],
   "objects": [
     {"key": "regulars", "title": "завсегдатаи у очага", "description": "Трое стариков вполголоса обсуждают свет в башне.",
      "approaches": [
        {"action": "explore", "title": "уловить тихий разговор", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "Старики шепчут: свет в башне появляется после полуночи, а дверь в подвал заперта с весны."}},
        {"action": "interact", "title": "разговорить завсегдатаев", "attribute": "will", "skill": "persuasion", "difficulty": 5, "on_success": {"fact": "Старики шепчут: свет в башне появляется после полуночи, а дверь в подвал заперта с весны."}}
      ]},
     {"key": "cellar_hatch", "title": "люк в погреб", "description": "Люк за стойкой; засов заело.", "state": "closed",
      "approaches": [{"action": "interact", "title": "сдвинуть заевший засов", "attribute": "strength", "skill": "athletics", "difficulty": 5, "on_success": {"state": "open", "fact": "В погребе бочки и ящик с печатью Гильдии теней."}}]}
   ]},
  {"name": "Зал Серой башни", "description": "Пыльный зал с обвалившейся лестницей; вниз ведёт узкий ход в подвал.", "difficulty": 0,
   "links": [{"to": "Рыночная площадь", "title": "на площадь"}, {"to": "Подвал башни", "title": "вниз в подвал"}],
   "objects": [
     {"key": "statue", "title": "статуя рыцаря", "description": "Каменный рыцарь со сколотым щитом.",
      "approaches": [{"action": "explore", "title": "найти стёртую карту на щите", "attribute": "wits", "skill": "awareness", "difficulty": 5, "on_success": {"fact": "На щите статуи выцарапана карта: крест под подвалом башни."}}]}
   ]},
  {"name": "Подвал башни", "description": "Сырой подвал, запертая дверь в глубине и следы сапог на пыли.", "difficulty": 1,
   "links": [{"to": "Зал Серой башни", "title": "наверх в зал"}],
   "objects": [
     {"key": "cellar_door", "title": "запертая дверь", "description": "Тяжёлая дверь с новым замком.", "state": "locked",
      "approaches": [
        {"action": "interact", "title": "выбить", "attribute": "strength", "skill": "athletics", "difficulty": 4, "on_success": {"state": "broken"}, "on_fail": {"condition": "exhausted"}},
        {"action": "interact", "title": "отпереть", "attribute": "agility", "skill": "thievery", "difficulty": 4, "on_success": {"state": "open"}},
        {"action": "explore", "title": "найти потайной рычаг", "attribute": "wits", "skill": "awareness", "difficulty": 4, "on_success": {"reveal": "hidden_lever"}}
      ]},
     {"key": "boot_tracks", "title": "следы сапог", "description": "Отпечатки сапог на пыли: от лестницы к двери и дальше.",
      "approaches": [{"action": "explore", "title": "восстановить путь по следам", "attribute": "wits", "skill": "survival", "difficulty": 4, "on_success": {"fact": "Следы сапог ведут от лестницы к запертой двери и обрываются у стены справа: там кто-то подолгу стоял."}}]},
     {"key": "hidden_lever", "title": "потайной рычаг", "description": "Рычаг в нише за камнем.", "hidden_until_revealed": true,
      "approaches": [{"action": "interact", "title": "дёрнуть рычаг", "difficulty": 0, "on_success": {"object": "cellar_door", "state": "open"}}]}
   ]}
]
$json$::jsonb) WHERE code = 'fantasy';

-- Локации уже созданных игр: описания, сложность и объекты из нового шаблона по названию локации; у объектов
-- сохраняются текущие state и revealed, новые объекты шаблона добавляются.
-- +goose StatementBegin
DO $$
DECLARE
  l record;
  tl jsonb;
  o jsonb;
  old jsonb;
  newobjs jsonb;
BEGIN
  FOR l IN
    SELECT loc.id, loc.name, loc.objects, ge.template
    FROM locations loc JOIN games gm ON gm.id = loc.game_id JOIN genres ge ON ge.id = gm.genre_id
  LOOP
    SELECT t INTO tl FROM jsonb_array_elements(coalesce(l.template->'locations', '[]'::jsonb)) t WHERE t->>'name' = l.name LIMIT 1;
    IF tl IS NULL THEN
      CONTINUE;
    END IF;
    newobjs := '[]'::jsonb;
    FOR o IN SELECT x FROM jsonb_array_elements(coalesce(tl->'objects', '[]'::jsonb)) x LOOP
      SELECT x INTO old FROM jsonb_array_elements(l.objects) x WHERE x->>'key' = o->>'key' LIMIT 1;
      IF old IS NOT NULL THEN
        IF old ? 'state' THEN o := o || jsonb_build_object('state', old->'state'); END IF;
        IF old ? 'revealed' THEN o := o || jsonb_build_object('revealed', old->'revealed'); END IF;
      END IF;
      newobjs := newobjs || jsonb_build_array(o);
    END LOOP;
    UPDATE locations
       SET objects = newobjs,
           description = coalesce(tl->>'description', description),
           difficulty = coalesce((tl->>'difficulty')::int, difficulty)
     WHERE id = l.id;
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Данные версии 0 не восстанавливаются (ADR-0014: один алгоритм); откатываются только значения по умолчанию.
ALTER TABLE games ALTER COLUMN rules_version SET DEFAULT 0;
ALTER TABLE character_models ALTER COLUMN rules_version SET DEFAULT 0;
