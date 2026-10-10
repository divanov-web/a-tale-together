-- +goose Up
-- Кворум раунда в настройках существующих игр: меньше заявок — раунд пропускается.
UPDATE games SET settings = settings || '{"min_round_answers": 2}'::jsonb WHERE NOT settings ? 'min_round_answers';

-- +goose Down
UPDATE games SET settings = settings - 'min_round_answers';
