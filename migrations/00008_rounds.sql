-- +goose Up
-- Пункт 05: раунды, заявки, outbox игровых сообщений (ADR-0006, ADR-0009).
CREATE TABLE rounds (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id     bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    season_id   bigint NOT NULL REFERENCES seasons (id),
    round_no    int NOT NULL,
    status      text NOT NULL DEFAULT 'open',
    opens_at    timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL,
    closed_at   timestamptz,
    seed        bigint NOT NULL DEFAULT 0,
    snapshot    jsonb,
    skip_reason text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (game_id, round_no)
);
-- У игры не более одного открытого раунда.
CREATE UNIQUE INDEX rounds_game_open_idx ON rounds (game_id) WHERE status = 'open';

CREATE TABLE action_submissions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id             bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id            bigint NOT NULL REFERENCES rounds (id) ON DELETE CASCADE,
    character_id        bigint NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    raw_text            text NOT NULL,
    status              text NOT NULL DEFAULT 'pending',
    telegram_message_id bigint,
    submitted_at        timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (round_id, character_id)
);
CREATE INDEX action_submissions_game_idx ON action_submissions (game_id);

CREATE TABLE messages_outbox (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    game_id             bigint NOT NULL REFERENCES games (id) ON DELETE CASCADE,
    round_id            bigint REFERENCES rounds (id) ON DELETE SET NULL,
    telegram_chat_id    bigint NOT NULL,
    kind                text NOT NULL,
    part                int NOT NULL DEFAULT 1,
    text                text NOT NULL,
    status              text NOT NULL DEFAULT 'pending',
    telegram_message_id bigint,
    attempts            int NOT NULL DEFAULT 0,
    send_after          timestamptz NOT NULL DEFAULT now(),
    claimed_at          timestamptz,
    last_error          text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz
);
CREATE INDEX messages_outbox_game_idx ON messages_outbox (game_id);
CREATE INDEX messages_outbox_pending_idx ON messages_outbox (send_after) WHERE status = 'pending';
-- Повтор сценария раунда не создаёт второе сообщение того же вида.
CREATE UNIQUE INDEX messages_outbox_dedup_idx ON messages_outbox (round_id, telegram_chat_id, kind, part) WHERE round_id IS NOT NULL;

-- +goose Down
DROP TABLE messages_outbox;
DROP TABLE action_submissions;
DROP TABLE rounds;
