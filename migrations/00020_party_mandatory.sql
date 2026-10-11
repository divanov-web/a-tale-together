-- +goose Up
-- ADR-0017: группа обязательна и управляется кодом; лимит один — settings.max_parties, он же лимит занятых локаций.
-- Настройка max_active_locations удалена из games.settings.
UPDATE games SET settings = settings - 'max_active_locations';

-- +goose Down
UPDATE games SET settings = jsonb_set(settings, '{max_active_locations}', '4'::jsonb) WHERE NOT settings ? 'max_active_locations';
