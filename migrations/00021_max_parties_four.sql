-- +goose Up
-- Лимит групп 4 (решение руководителя 2026-10-11): группа обязательна и держит одну локацию, лимит задаёт и число
-- занятых локаций (ADR-0017). Существующие игры переводятся с 3 на 4; явно изменённые значения не трогаются.
UPDATE games SET settings = jsonb_set(settings, '{max_parties}', '4'::jsonb) WHERE (settings->>'max_parties')::int = 3;

-- +goose Down
UPDATE games SET settings = jsonb_set(settings, '{max_parties}', '3'::jsonb) WHERE (settings->>'max_parties')::int = 4;
