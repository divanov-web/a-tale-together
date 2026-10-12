-- Вайп песочницы: удаляет все игры и связанные с ними данные, оставляет справочники.
-- Запуск: make wipe (останавливает бота, выполняет этот файл, чистит logs/, запускает бота).
-- Сохраняются: users, chats, chat_moderators, app_settings, llm_profiles, genres, character_models, goose_db_version.
-- Новая таблица с колонкой game_id → добавить DELETE ниже (проверка в конце файла упадёт, если забыть).
BEGIN;

UPDATE users SET current_game_id = NULL;
UPDATE characters SET party_id = NULL;
UPDATE parties SET leader_character_id = NULL;

DELETE FROM generation_jobs;
DELETE FROM faction_goals;
DELETE FROM quest_stages;
DELETE FROM quests;
DELETE FROM location_discoveries;
DELETE FROM items;
DELETE FROM npcs;
DELETE FROM world_objects;
DELETE FROM resolved_actions;
DELETE FROM action_submissions;
DELETE FROM world_events;
DELETE FROM messages_outbox;
DELETE FROM llm_runs;
DELETE FROM character_stats;
DELETE FROM parties;
DELETE FROM rounds;
DELETE FROM characters;
DELETE FROM game_members;
DELETE FROM locations;
DELETE FROM seasons;
DELETE FROM factions;
DELETE FROM games;
DELETE FROM dialogs;
DELETE FROM telegram_updates;

ALTER TABLE games ALTER COLUMN id RESTART WITH 1;
ALTER TABLE seasons ALTER COLUMN id RESTART WITH 1;
ALTER TABLE factions ALTER COLUMN id RESTART WITH 1;
ALTER TABLE locations ALTER COLUMN id RESTART WITH 1;
ALTER TABLE world_objects ALTER COLUMN id RESTART WITH 1;
ALTER TABLE items ALTER COLUMN id RESTART WITH 1;
ALTER TABLE npcs ALTER COLUMN id RESTART WITH 1;
ALTER TABLE location_discoveries ALTER COLUMN id RESTART WITH 1;
ALTER TABLE generation_jobs ALTER COLUMN id RESTART WITH 1;
ALTER TABLE quests ALTER COLUMN id RESTART WITH 1;
ALTER TABLE quest_stages ALTER COLUMN id RESTART WITH 1;
ALTER TABLE faction_goals ALTER COLUMN id RESTART WITH 1;
ALTER TABLE characters ALTER COLUMN id RESTART WITH 1;
ALTER TABLE parties ALTER COLUMN id RESTART WITH 1;
ALTER TABLE rounds ALTER COLUMN id RESTART WITH 1;
ALTER TABLE action_submissions ALTER COLUMN id RESTART WITH 1;
ALTER TABLE resolved_actions ALTER COLUMN id RESTART WITH 1;
ALTER TABLE world_events ALTER COLUMN id RESTART WITH 1;
ALTER TABLE messages_outbox ALTER COLUMN id RESTART WITH 1;
ALTER TABLE llm_runs ALTER COLUMN id RESTART WITH 1;

-- Контроль полноты: ни в одной таблице с колонкой game_id не должно остаться строк.
DO $$
DECLARE t text; n bigint;
BEGIN
  FOR t IN SELECT table_name FROM information_schema.columns
           WHERE table_schema = 'public' AND column_name = 'game_id' LOOP
    EXECUTE format('SELECT count(*) FROM %I', t) INTO n;
    IF n > 0 THEN
      RAISE EXCEPTION 'wipe: в таблице % осталось % строк — добавить DELETE в scripts/wipe-sandbox.sql', t, n;
    END IF;
  END LOOP;
END $$;

COMMIT;
