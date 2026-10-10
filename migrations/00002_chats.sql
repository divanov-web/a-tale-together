-- +goose Up
-- Пункт 02: открытие лички, чаты с ботом, модераторы, состояние диалогов (ADR-0007, ADR-0008).
ALTER TABLE users ADD COLUMN started_at timestamptz;

CREATE TABLE chats (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    telegram_chat_id bigint NOT NULL UNIQUE,
    type             text NOT NULL,
    title            text NOT NULL DEFAULT '',
    owner_user_id    bigint NOT NULL REFERENCES users (id),
    bot_status       text NOT NULL DEFAULT 'member',
    added_at         timestamptz NOT NULL DEFAULT now(),
    left_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_moderators (
    chat_id            bigint NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    user_id            bigint NOT NULL REFERENCES users (id),
    granted_by_user_id bigint NOT NULL REFERENCES users (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chat_id, user_id)
);

-- Диалог в личке: у пользователя не более одного; данные шага в jsonb.
CREATE TABLE dialogs (
    user_id    bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    step       text NOT NULL,
    game_id    bigint,
    data       jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE dialogs;
DROP TABLE chat_moderators;
DROP TABLE chats;
ALTER TABLE users DROP COLUMN started_at;
