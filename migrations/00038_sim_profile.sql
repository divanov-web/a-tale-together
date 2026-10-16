-- +goose Up
-- Пункт 40 (ADR-0034): профиль прогонов имитатора — haiku для всех ролей по подписке claude-cli; любая модель
-- подменяется флагами `cmd/simulate`. Глобальный профиль по умолчанию не меняется: имитатор ставит свой в БД прогона.
INSERT INTO llm_profiles (name, provider, model_interpreter, model_narrator, params)
VALUES ('sim-haiku', 'claude-cli', 'haiku', 'haiku', '{"effort": "low", "max_budget_usd": 0.5, "timeout_seconds": 180, "parallelism": 2}'::jsonb)
ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM llm_profiles WHERE name = 'sim-haiku';
