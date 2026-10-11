-- +goose Up
-- Завязка истории от рассказчика при /begin (ADR-0018): текст стартовой сцены хранится у игры и уходит выжимкой
-- в контекст хроник. У уже запущенных игр пусто: они начинались со сцены шаблона жанра.
ALTER TABLE games ADD COLUMN opening_scene text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE games DROP COLUMN opening_scene;
