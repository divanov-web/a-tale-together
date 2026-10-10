-- +goose Up
-- Решение руководителя 2026-10-10: профиль local-claude переводится на sonnet (интерпретатор) и opus (рассказчик).
UPDATE llm_profiles SET model_interpreter = 'sonnet', model_narrator = 'opus', updated_at = now() WHERE name = 'local-claude';

-- +goose Down
UPDATE llm_profiles SET model_interpreter = 'haiku', model_narrator = 'sonnet', updated_at = now() WHERE name = 'local-claude';
