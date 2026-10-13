-- +goose Up
-- Промежуточные статусы игры `starting` и `finishing` (B34, ADR-0024): /begin и /finish только меняют статус,
-- стартовую сцену и финальную хронику делает тик планировщика.
ALTER TABLE games DROP CONSTRAINT games_status_check;
ALTER TABLE games ADD CONSTRAINT games_status_check CHECK (status IN ('setup', 'draft', 'starting', 'active', 'paused', 'finishing', 'finished'));

-- +goose Down
ALTER TABLE games DROP CONSTRAINT games_status_check;
ALTER TABLE games ADD CONSTRAINT games_status_check CHECK (status IN ('setup', 'draft', 'active', 'paused', 'finished'));
