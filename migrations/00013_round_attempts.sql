-- +goose Up
-- Пункт 10 (ADR-0013): учёт неудачных попыток закрыть раунд вне транзакции и метрики разрешения.
ALTER TABLE rounds ADD COLUMN attempts int NOT NULL DEFAULT 0;
ALTER TABLE rounds ADD COLUMN metrics jsonb;

-- +goose Down
ALTER TABLE rounds DROP COLUMN metrics;
ALTER TABLE rounds DROP COLUMN attempts;
