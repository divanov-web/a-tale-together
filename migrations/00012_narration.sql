-- +goose Up
-- Пункт 09: ответ рассказчика (общий текст и блоки по сценам) хранится у раунда; выжимка последних хроник идёт в контекст следующего вызова.
ALTER TABLE rounds ADD COLUMN narration jsonb;

-- +goose Down
ALTER TABLE rounds DROP COLUMN narration;
