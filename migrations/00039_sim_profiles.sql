-- +goose Up
-- Профили прогонов имитатора (ADR-0034, решение руководителя 2026-10-17): конфигурация моделей всех ролей,
-- включая ИИ-игрока (`params.player_model`), живёт в профиле, чтобы `llm_runs`, журнал и сводка прогона показывали
-- реальную конфигурацию. `sim-haiku` — всё haiku; `sim-sonnet` — всё sonnet; `sim-opus-narrator` — рассказчик,
-- завязка и мир на opus, интерпретатор и игроки на sonnet. Новые профили добавляются миграцией, редко.
UPDATE llm_profiles SET params = params || '{"player_model": "haiku"}'::jsonb, updated_at = now()
WHERE name = 'sim-haiku' AND NOT (params ? 'player_model');

INSERT INTO llm_profiles (name, provider, model_interpreter, model_narrator, params)
VALUES
  ('sim-sonnet', 'claude-cli', 'sonnet', 'sonnet', '{"effort": "low", "max_budget_usd": 0.5, "timeout_seconds": 180, "parallelism": 2, "player_model": "sonnet"}'::jsonb),
  ('sim-opus-narrator', 'claude-cli', 'sonnet', 'opus', '{"effort": "low", "max_budget_usd": 0.5, "timeout_seconds": 180, "parallelism": 2, "player_model": "sonnet"}'::jsonb)
ON CONFLICT (name) DO NOTHING;

-- +goose Down
DELETE FROM llm_profiles WHERE name IN ('sim-sonnet', 'sim-opus-narrator');
UPDATE llm_profiles SET params = params - 'player_model' WHERE name = 'sim-haiku';
