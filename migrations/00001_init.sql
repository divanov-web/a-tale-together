-- +goose Up
-- Дедупликация входящих update Telegram: повтор update_id не обрабатывается.
CREATE TABLE telegram_updates (
    update_id   bigint PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- Пользователи Telegram, когда-либо писавшие боту (ADR-0005: bigint identity).
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    telegram_id   bigint NOT NULL UNIQUE,
    username      text,
    first_name    text NOT NULL DEFAULT '',
    language_code text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
DROP TABLE telegram_updates;
