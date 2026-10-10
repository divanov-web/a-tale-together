-- +goose Up
-- Лимит игроков мира заменён лимитом игроков, обрабатываемых за раунд (max_round_players); ключ переименован, значение сохранено.
UPDATE games SET settings = (settings - 'max_players') || jsonb_build_object('max_round_players', COALESCE(settings->'max_players', '20'::jsonb));
-- +goose Down
UPDATE games SET settings = (settings - 'max_round_players') || jsonb_build_object('max_players', COALESCE(settings->'max_round_players', '20'::jsonb));
