-- +goose Up
-- Пункт 06: профили LLM, глобальные настройки бота, профиль игры, учёт вызовов (ADR-0003).
CREATE TABLE llm_profiles (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name              text NOT NULL UNIQUE,
    provider          text NOT NULL,
    model_interpreter text NOT NULL,
    model_narrator    text NOT NULL,
    params            jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled           boolean NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE app_settings (
    key        text PRIMARY KEY,
    value      text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE games ADD COLUMN llm_profile_id bigint REFERENCES llm_profiles (id) ON DELETE SET NULL;

CREATE TABLE llm_runs (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id        bigint REFERENCES games (id) ON DELETE CASCADE, -- NULL для проверки профиля вне игры
    round_id       bigint REFERENCES rounds (id) ON DELETE SET NULL,
    profile_id     bigint NOT NULL REFERENCES llm_profiles (id),
    provider       text NOT NULL,
    role           text NOT NULL,
    model          text NOT NULL,
    prompt_version text NOT NULL DEFAULT '',
    tokens_in      int NOT NULL DEFAULT 0,
    tokens_out     int NOT NULL DEFAULT 0,
    cost_usd       numeric(12, 6) NOT NULL DEFAULT 0,
    subscription   boolean NOT NULL DEFAULT false,
    duration_ms    bigint NOT NULL DEFAULT 0,
    error          text NOT NULL DEFAULT '',
    request_id     text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_runs_game_idx ON llm_runs (game_id);
CREATE INDEX llm_runs_created_idx ON llm_runs (created_at);

-- Стартовый профиль: локальный Claude Code по подписке; модели по псевдонимам CLI.
INSERT INTO llm_profiles (name, provider, model_interpreter, model_narrator, params)
VALUES ('local-claude', 'claude-cli', 'haiku', 'sonnet', '{"effort": "low", "max_budget_usd": 0.5, "timeout_seconds": 120, "parallelism": 2}'::jsonb);
INSERT INTO app_settings (key, value) VALUES ('default_llm_profile', 'local-claude');

-- +goose Down
DROP TABLE llm_runs;
ALTER TABLE games DROP COLUMN llm_profile_id;
DROP TABLE app_settings;
DROP TABLE llm_profiles;
