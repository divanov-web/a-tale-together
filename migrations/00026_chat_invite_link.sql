-- +goose Up
-- Ссылка-приглашение в группу для кнопки «Перейти в чат игры» (B24, ADR-0022): создаётся ботом один раз,
-- очищается при любом my_chat_member; пусто — ссылки нет.
ALTER TABLE chats ADD COLUMN invite_link text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chats DROP COLUMN invite_link;
