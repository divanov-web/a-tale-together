-- +goose Up
-- Пункт 33 (ADR-0032): тарифы моделей в БД и токены по видам в учёте вызовов.
-- Тариф — USD за 1M токенов по видам; запись кэша — тариф пятиминутного кэша; строка задаёт полное имя модели
-- или префикс для датированных снимков (`claude-opus-5-5-2026…` → `claude-opus-5-5`).
CREATE TABLE llm_prices (
    model           text PRIMARY KEY,
    input_usd       numeric(12, 6) NOT NULL,
    cache_write_usd numeric(12, 6) NOT NULL,
    cache_read_usd  numeric(12, 6) NOT NULL,
    output_usd      numeric(12, 6) NOT NULL,
    source          text NOT NULL DEFAULT '',
    priced_at       date NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Тарифы Anthropic со страницы цен, сверено 2026-10-15. Haiku 5.5 — тариф запросов до 100K токенов.
INSERT INTO llm_prices (model, input_usd, cache_write_usd, cache_read_usd, output_usd, source, priced_at) VALUES
    ('claude-opus-5-5',   4.00,  5.00,  0.20, 20.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-sonnet-5-5', 2.00,  2.50,  0.10, 10.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-haiku-5-5',  0.10,  0.125, 0.01,  0.50, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-fable-5-1', 10.00, 12.50,  0.25, 50.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-opus-5',     5.00,  6.25,  0.50, 25.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-opus-4-8',   5.00,  6.25,  0.50, 25.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15'),
    ('claude-sonnet-5',   2.00,  2.50,  0.20, 10.00, 'https://platform.claude.com/docs/en/about-claude/pricing', '2026-10-15');

-- Учёт вызовов: токены по видам (tokens_in остаётся суммой вход + запись кэша + чтение кэша) и оценка провайдера отдельно.
ALTER TABLE llm_runs
    ADD COLUMN tokens_cache_write int NOT NULL DEFAULT 0,
    ADD COLUMN tokens_cache_read  int NOT NULL DEFAULT 0,
    ADD COLUMN cost_provider_usd  numeric(12, 6) NOT NULL DEFAULT 0;
-- У прежних строк cost_usd была оценкой claude -p; разбивки токенов нет, пересчёт по таблице невозможен — оценка переносится в обе колонки.
UPDATE llm_runs SET cost_provider_usd = cost_usd;

-- +goose Down
ALTER TABLE llm_runs DROP COLUMN cost_provider_usd, DROP COLUMN tokens_cache_read, DROP COLUMN tokens_cache_write;
DROP TABLE llm_prices;
