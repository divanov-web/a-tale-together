-- +goose Up
-- Кнопки под игровым сообщением (пункт 18, ADR-0015): JSON рядов кнопок model.Keyboard; NULL — без кнопок.
ALTER TABLE messages_outbox ADD COLUMN keyboard jsonb;

-- +goose Down
ALTER TABLE messages_outbox DROP COLUMN keyboard;
