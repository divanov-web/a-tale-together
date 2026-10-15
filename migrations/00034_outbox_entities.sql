-- +goose Up
-- Разметка игровых сообщений outbox (B40, ADR-0030): отрезки entities в рунах, как в `model.FormattedText`; NULL — обычный текст.
ALTER TABLE messages_outbox ADD COLUMN entities jsonb;

-- +goose Down
ALTER TABLE messages_outbox DROP COLUMN entities;
